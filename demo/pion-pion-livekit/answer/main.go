package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"webrtc-demo/pkg/config"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

func signalCandidate(addr string, c *webrtc.ICECandidate) error {
	fmt.Println(c)

	payload := []byte(c.ToJSON().Candidate)
	resp, err := http.Post(fmt.Sprintf("http://%s/candidate", addr), // nolint:noctx
		"application/json; charset=utf-8", bytes.NewReader(payload))
	if err != nil {
		return err
	}

	if closeErr := resp.Body.Close(); closeErr != nil {
		return closeErr
	}

	return nil
}

var (
	rtpChan = make(chan *rtp.Packet)

	rtpBinChan = make(chan []byte)
)

func main() { // nolint:gocognit
	offerAddr := flag.String("offer-address", "localhost:50000", "Address that the Offer HTTP server is hosted on.")
	answerAddr := flag.String("answer-address", ":60000", "Address that the Answer HTTP server is hosted on.")
	flag.Parse()

	var candidatesMux sync.Mutex
	pendingCandidates := make([]*webrtc.ICECandidate, 0)
	// Everything below is the Pion WebRTC API! Thanks for using it ❤️.

	// Prepare the configuration
	webrtcConfig := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	}

	// Create a new RTCPeerConnection
	peerConnection, err := webrtc.NewPeerConnection(webrtcConfig)
	if err != nil {
		panic(err)
	}
	defer func() {
		if err := peerConnection.Close(); err != nil {
			fmt.Printf("cannot close peerConnection: %v\n", err)
		}
	}()

	// When an ICE candidate is available send to the other Pion instance
	// the other Pion instance will add this candidate by calling AddICECandidate
	peerConnection.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}

		candidatesMux.Lock()
		defer candidatesMux.Unlock()

		desc := peerConnection.RemoteDescription()
		if desc == nil {
			pendingCandidates = append(pendingCandidates, c)
		} else if onICECandidateErr := signalCandidate(*offerAddr, c); onICECandidateErr != nil {
			panic(onICECandidateErr)
		}
	})

	// A HTTP handler that allows the other Pion instance to send us ICE candidates
	// This allows us to add ICE candidates faster, we don't have to wait for STUN or TURN
	// candidates which may be slower
	http.HandleFunc("/candidate", func(w http.ResponseWriter, r *http.Request) {
		candidate, candidateErr := ioutil.ReadAll(r.Body)
		if candidateErr != nil {
			panic(candidateErr)
		}
		if candidateErr := peerConnection.AddICECandidate(webrtc.ICECandidateInit{Candidate: string(candidate)}); candidateErr != nil {
			panic(candidateErr)
		}
	})

	// A HTTP handler that processes a SessionDescription given to us from the other Pion process
	http.HandleFunc("/sdp", func(w http.ResponseWriter, r *http.Request) {
		sdp := webrtc.SessionDescription{}
		if err := json.NewDecoder(r.Body).Decode(&sdp); err != nil {
			panic(err)
		}

		// fmt.Println(sdp)
		if err := peerConnection.SetRemoteDescription(sdp); err != nil {
			panic(err)
		}

		// Create an answer to send to the other process
		answer, err := peerConnection.CreateAnswer(nil)
		if err != nil {
			panic(err)
		}
		// fmt.Println(answer)

		// Send our answer to the HTTP server listening in the other process
		payload, err := json.Marshal(answer)
		if err != nil {
			panic(err)
		}
		resp, err := http.Post(fmt.Sprintf("http://%s/sdp", *offerAddr), "application/json; charset=utf-8", bytes.NewReader(payload)) // nolint:noctx
		if err != nil {
			panic(err)
		} else if closeErr := resp.Body.Close(); closeErr != nil {
			panic(closeErr)
		}

		// Sets the LocalDescription, and starts our UDP listeners
		err = peerConnection.SetLocalDescription(answer)
		if err != nil {
			panic(err)
		}

		candidatesMux.Lock()
		for _, c := range pendingCandidates {
			onICECandidateErr := signalCandidate(*offerAddr, c)
			if onICECandidateErr != nil {
				panic(onICECandidateErr)
			}
		}
		candidatesMux.Unlock()
	})

	// Set the handler for Peer connection state
	// This will notify you when the peer has connected/disconnected
	peerConnection.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		fmt.Printf("Peer Connection State has changed: %s\n", s.String())

		if s == webrtc.PeerConnectionStateFailed {
			// Wait until PeerConnection has had no network activity for 30 seconds or another failure. It may be reconnected using an ICE Restart.
			// Use webrtc.PeerConnectionStateDisconnected if you are interested in detecting faster timeout.
			// Note that the PeerConnection may come back from PeerConnectionStateDisconnected.
			fmt.Println("Peer Connection has gone to failed exiting")
			os.Exit(0)
		}
	})

	roomConfig := config.Config{
		Host:      "ws://localhost:7880",
		ApiKey:    "APInAy27RUmYUnV",
		ApiSecret: "90jQt67cwele8a6uIuIQLK0ZJ0cJKXnzz6iEI8h43dO",
		Identity:  "get-sdp",
		RoomName:  "stark-tower",
		Token:     "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJleHAiOjE2ODc4MjIyNjMsImlzcyI6IkFQSW5BeTI3UlVtWVVuViIsImp0aSI6InRvbnlfc3RhcmsiLCJuYW1lIjoiVG9ueSBTdGFyadsyIsIm5iZiI6MTY1MTgyMjI2Mywic3ViIjoidG9ueV9zdGFyayIsInZpZGVvIjp7InJvb20iOiJzdGFyay10b3dlciIsInJvb21Kb2luIjp0cnVlfX0.XCuS0Rw73JI8vE6dBUD3WbYGFNz1zGzdUBaDmnuI9Aw",
	}

	room, err := lksdk.ConnectToRoom(roomConfig.Host, lksdk.ConnectInfo{
		APIKey:              roomConfig.ApiKey,
		APISecret:           roomConfig.ApiSecret,
		RoomName:            roomConfig.RoomName,
		ParticipantIdentity: roomConfig.Identity,
	})
	if err != nil {
		panic(err)
	}

	track, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264}, "video", "test_id")
	if err != nil {
		log.Fatal(err)
	}

	trackPublication, err := room.LocalParticipant.PublishTrack(track, &lksdk.TrackPublicationOptions{
		Name:        "my test h264 track",
		Source:      livekit.TrackSource_CAMERA,
		VideoWidth:  1920,
		VideoHeight: 1080,
	})
	fmt.Println(trackPublication.Name())

	participants := []string{}
	for _, p := range room.GetParticipants() {
		participants = append(participants, p.SID())
	}

	go func() {
		for {
			if err := room.LocalParticipant.PublishData([]byte("test data"), livekit.DataPacket_RELIABLE, participants); err != nil {
				panic(err)
			}
			time.Sleep(time.Second)
		}

	}()

	peerConnectionPublisher := room.LocalParticipant.GetPublisherPeerConnection()
	roomRtpSender, err := peerConnectionPublisher.AddTrack(track)
	if err != nil {
		panic(err)
	}
	go func() {
		buf := make([]byte, 1500)
		for {
			if _, _, err := roomRtpSender.Read(buf); err != nil {
				return
			}
		}
	}()

	go func() {
		for {
			if _, err := track.Write(<-rtpBinChan); err != nil {
				panic(err)
			}
			// if err := track.WriteRTP(<-rtpChan); err != nil {
			// 	panic(err)
			// }
		}
	}()

	// // Register data channel creation handling
	// peerConnection.OnDataChannel(func(d *webrtc.DataChannel) {
	// 	fmt.Printf("New DataChannel %s %d\n", d.Label(), d.ID())

	// 	// Register channel opening handling
	// 	d.OnOpen(func() {
	// 		fmt.Printf("Data channel '%s'-'%d' open. Random messages will now be sent to any connected DataChannels every 5 seconds\n", d.Label(), d.ID())

	// 		for range time.NewTicker(5 * time.Second).C {
	// 			message := signal.RandSeq(15)
	// 			fmt.Printf("Sending '%s'\n", message)

	// 			// Send the message as text
	// 			sendTextErr := d.SendText(message)
	// 			if sendTextErr != nil {
	// 				panic(sendTextErr)
	// 			}
	// 		}
	// 	})

	// 	// Register text message handling
	// 	d.OnMessage(func(msg webrtc.DataChannelMessage) {
	// 		fmt.Printf("Message from DataChannel '%s': '%s'\n", d.Label(), string(msg.Data))
	// 	})
	// })

	peerConnection.OnTrack(func(tr *webrtc.TrackRemote, r *webrtc.RTPReceiver) {
		codec := tr.Codec()
		fmt.Println("have track", codec.MimeType)

		switch codec.MimeType {
		case webrtc.MimeTypeH264:
			go func() {
				// addr := net.UDPAddr{
				// 	IP:   net.ParseIP("238.0.0.1"),
				// 	Port: 9000,
				// }
				// conn, err := net.ListenUDP("udp", nil)
				// if err != nil {
				// 	log.Fatal(err)
				// }

				for {
					// pack, _, err := tr.ReadRTP()
					// if err != nil {
					// 	if err == io.EOF {
					// 		fmt.Println(err)
					// 		return
					// 	}
					// 	fmt.Println(err)
					// 	continue
					// }
					// fmt.Println("have rtp packet", pack.PayloadType)
					// rtpChan <- pack

					buf := make([]byte, 1500)
					n, _, err := tr.Read(buf)
					if err != nil {
						panic(err)
					}

					rtpBinChan <- buf[:n]

					// bin, err := pack.Marshal()
					// if err != nil {
					// 	fmt.Println("failed to marshal packet", err)
					// } else {
					// 	if _, err := conn.WriteToUDP(bin, &addr); err != nil {
					// 		fmt.Println(err)
					// 	}
					// }
				}
			}()
		}
	})

	// Start HTTP server that accepts requests from the offer process to exchange SDP and Candidates
	panic(http.ListenAndServe(*answerAddr, nil))
}

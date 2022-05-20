package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
	"github.com/pion/webrtc/v3/pkg/media/h264reader"
)

func signalCandidate(addr string, c *webrtc.ICECandidate) error {
	payload := []byte(c.ToJSON().Candidate)
	resp, err := http.Post(fmt.Sprintf("http://%s/candidate", addr), "application/json; charset=utf-8", bytes.NewReader(payload)) //nolint:noctx
	if err != nil {
		return err
	}

	if closeErr := resp.Body.Close(); closeErr != nil {
		return closeErr
	}

	return nil
}

const (
	H264_FRAME_DURATION = time.Millisecond * 33

	FFMPEG_CMD = "ffmpeg -rtbufsize 100M -i ./media/never_gonna_give_you_up.mp4 -pix_fmt yuv420p -c:v libx264 -bsf:v h264_mp4toannexb -b:v 2M -max_delay 0 -bf 0 -f h264 -"
)

func main() { //nolint:gocognit
	offerAddr := flag.String("offer-address", ":50000", "Address that the Offer HTTP server is hosted on.")
	answerAddr := flag.String("answer-address", "127.0.0.1:60000", "Address that the Answer HTTP server is hosted on.")
	// videoFile := flag.String("video-file", "./media/never_gonna_give_you_up.mp4", "mp4 video filed")
	flag.Parse()

	var candidatesMux sync.Mutex
	pendingCandidates := make([]*webrtc.ICECandidate, 0)

	// Everything below is the Pion WebRTC API! Thanks for using it ❤️.

	// Prepare the configuration
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	}

	// Create a new RTCPeerConnection
	peerConnection, err := webrtc.NewPeerConnection(config)
	if err != nil {
		panic(err)
	}
	defer func() {
		if cErr := peerConnection.Close(); cErr != nil {
			fmt.Printf("cannot close peerConnection: %v\n", cErr)
		}
	}()

	// When an ICE candidate is available send to the other Pion instance
	// the other Pion instance will add this candidate by calling AddICECandidate
	peerConnection.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		fmt.Println("ice candidate:", c)

		candidatesMux.Lock()
		defer candidatesMux.Unlock()

		desc := peerConnection.RemoteDescription()
		if desc == nil {
			pendingCandidates = append(pendingCandidates, c)
		} else if onICECandidateErr := signalCandidate(*answerAddr, c); onICECandidateErr != nil {
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
		fmt.Println("add ice candidate:", string(candidate))
		if candidateErr := peerConnection.AddICECandidate(webrtc.ICECandidateInit{Candidate: string(candidate)}); candidateErr != nil {
			panic(candidateErr)
		}
	})

	// A HTTP handler that processes a SessionDescription given to us from the other Pion process
	http.HandleFunc("/sdp", func(w http.ResponseWriter, r *http.Request) {
		sdp := webrtc.SessionDescription{}
		if sdpErr := json.NewDecoder(r.Body).Decode(&sdp); sdpErr != nil {
			panic(sdpErr)
		}

		if sdpErr := peerConnection.SetRemoteDescription(sdp); sdpErr != nil {
			panic(sdpErr)
		}

		candidatesMux.Lock()
		defer candidatesMux.Unlock()

		for _, c := range pendingCandidates {
			if onICECandidateErr := signalCandidate(*answerAddr, c); onICECandidateErr != nil {
				panic(onICECandidateErr)
			}
		}
	})
	// Start HTTP server that accepts requests from the answer process
	go func() { panic(http.ListenAndServe(*offerAddr, nil)) }()

	// Create a datachannel with label 'data'
	// dataChannel, err := peerConnection.CreateDataChannel("data", nil)
	// if err != nil {
	// 	panic(err)
	// }

	track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264}, "video", "test_id")
	if err != nil {
		panic(err)
	}

	rtpSender, err := peerConnection.AddTrack(track)
	if err != nil {
		panic(err)
	}
	go func() {
		for {
			buf := make([]byte, 1500)
			if _, _, err := rtpSender.Read(buf); err != nil {
				return
			}
		}
	}()

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

	// // Register channel opening handling
	// dataChannel.OnOpen(func() {
	// 	fmt.Printf("Data channel '%s'-'%d' open. Random messages will now be sent to any connected DataChannels every 5 seconds\n", dataChannel.Label(), dataChannel.ID())

	// 	for range time.NewTicker(5 * time.Second).C {
	// 		message := signal.RandSeq(15)
	// 		fmt.Printf("Sending '%s'\n", message)

	// 		// Send the message as text
	// 		sendTextErr := dataChannel.SendText(message)
	// 		if sendTextErr != nil {
	// 			panic(sendTextErr)
	// 		}
	// 	}
	// })

	// // Register text message handling
	// dataChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
	// 	fmt.Printf("Message from DataChannel '%s': '%s'\n", dataChannel.Label(), string(msg.Data))
	// })

	// Create an offer to send to the other process
	offer, err := peerConnection.CreateOffer(&webrtc.OfferOptions{})
	if err != nil {
		panic(err)
	}
	// fmt.Println(offer)

	// Sets the LocalDescription, and starts our UDP listeners
	// Note: this will start the gathering of ICE candidates
	if err = peerConnection.SetLocalDescription(offer); err != nil {
		panic(err)
	}

	// Send our offer to the HTTP server listening in the other process
	payload, err := json.Marshal(offer)
	if err != nil {
		panic(err)
	}
	resp, err := http.Post(fmt.Sprintf("http://%s/sdp", *answerAddr), "application/json; charset=utf-8", bytes.NewReader(payload)) // nolint:noctx
	if err != nil {
		panic(err)
	} else if err := resp.Body.Close(); err != nil {
		panic(err)
	}

	go func() {
		cmdStr := strings.Split(FFMPEG_CMD, " ")

		cmd := exec.Command(cmdStr[0], cmdStr[1:]...)
		dataPipe, err := cmd.StdoutPipe()
		if err != nil {
			log.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			log.Fatal(err)
		}

		h264Reader, err := h264reader.NewReader(dataPipe)
		if err != nil {
			log.Fatal(err)
		}

		spsAndPpsCache := []byte{}
		ticker := time.NewTicker(H264_FRAME_DURATION)

		for {
			<-ticker.C

			nal, err := h264Reader.NextNAL()
			if err != nil {
				if err == io.EOF {
					fmt.Println("All video frames parsed and sent")
					os.Exit(0)
				}
				log.Fatal(err)
			}
			nal.Data = append([]byte{0x00, 0x00, 0x00, 0x01}, nal.Data...)

			if nal.UnitType == h264reader.NalUnitTypeSPS || nal.UnitType == h264reader.NalUnitTypePPS {
				spsAndPpsCache = append(spsAndPpsCache, nal.Data...)
				continue
			} else if nal.UnitType == h264reader.NalUnitTypeCodedSliceIdr {
				nal.Data = append(spsAndPpsCache, nal.Data...)
				spsAndPpsCache = []byte{}
			}

			if err := track.WriteSample(media.Sample{
				Data:     nal.Data,
				Duration: time.Second,
			}); err != nil {
				log.Fatal(err)
			}
		}
	}()

	// Block forever
	select {}
}

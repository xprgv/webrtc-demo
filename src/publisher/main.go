package main

import (
	"fmt"
	"log"
	"net"

	"github.com/pion/webrtc/v3"
)

func main() {
	peerConnection, err := webrtc.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5500})
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := listener.Close(); err != nil {
			log.Fatal(err)
		}
	}()

	h264Track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264}, "video", "webrtc-pion-demo")
	if err != nil {
		log.Fatal(err)
	}

	rtpSender, err := peerConnection.AddTrack(h264Track)
	if err != nil {
		log.Fatal(err)
	}

	go func() {
		buf := make([]byte, 1500)
		for {
			if _, _, err := rtpSender.Read(buf); err != nil {
				return
			}
		}
	}()

	peerConnection.OnICEConnectionStateChange(func(is webrtc.ICEConnectionState) {
		fmt.Printf("Connection State has changed %s \n", is.String())

		if is == webrtc.ICEConnectionStateFailed {
			if closeErr := peerConnection.Close(); closeErr != nil {
				panic(closeErr)
			}
		}
	})

}

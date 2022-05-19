package pc

import "github.com/pion/webrtc/v3"

type PeerConnection struct {
	pc *webrtc.PeerConnection

	SdpOfferChan        chan webrtc.SessionDescription
	SdpAnswerChan       chan webrtc.SessionDescription
	IceCandidateChan    chan webrtc.ICECandidate
	IceCandidateStrChan chan string
}

func New(config webrtc.Configuration) {}

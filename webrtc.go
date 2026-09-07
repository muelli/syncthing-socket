package socket

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"time"

	"github.com/pion/webrtc/v4"
)

type SignalMessage struct {
	Type      string                   `json:"type"`                // "offer", "answer", "candidate", "fallback"
	SDP       string                   `json:"sdp,omitempty"`       
	Candidate *webrtc.ICECandidateInit `json:"candidate,omitempty"` 
}

func sendSignal(conn net.Conn, msg SignalMessage) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(b)))
	if _, err := conn.Write(lenBuf); err != nil {
		return err
	}
	_, err = conn.Write(b)
	return err
}

func readSignal(conn net.Conn) (SignalMessage, error) {
	var msg SignalMessage
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(conn, lenBuf); err != nil {
		return msg, err
	}
	l := binary.BigEndian.Uint32(lenBuf)
	if l > 10*1024*1024 { // 10MB sanity check
		return msg, fmt.Errorf("message too large")
	}
	b := make([]byte, l)
	if _, err := io.ReadFull(conn, b); err != nil {
		return msg, err
	}
	err := json.Unmarshal(b, &msg)
	return msg, err
}

func getWebRTCConfig() webrtc.Configuration {
	return webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.syncthing.net:3478", "stun:stun.l.google.com:19302"},
			},
		},
	}
}

func logICEConnection(pc *webrtc.PeerConnection) {
	if pc == nil || pc.SCTP() == nil || pc.SCTP().Transport() == nil || pc.SCTP().Transport().ICETransport() == nil {
		return
	}
	cp, err := pc.SCTP().Transport().ICETransport().GetSelectedCandidatePair()
	if err == nil && cp != nil {
		slog.Info("WebRTC ICE Connected",
			"local", fmt.Sprintf("%s:%d (%s)", cp.Local.Address, cp.Local.Port, cp.Local.Typ.String()),
			"remote", fmt.Sprintf("%s:%d (%s)", cp.Remote.Address, cp.Remote.Port, cp.Remote.Typ.String()),
		)
	}
}

type WebRTCConn struct {
	io.ReadWriteCloser
	pc *webrtc.PeerConnection
}

func (c *WebRTCConn) LocalAddr() net.Addr {
	return &net.TCPAddr{}
}

func (c *WebRTCConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{}
}

func (c *WebRTCConn) SetDeadline(t time.Time) error {
	return nil
}

func (c *WebRTCConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (c *WebRTCConn) SetWriteDeadline(t time.Time) error {
	return nil
}

// bufferedAmounter is satisfied by pion's detached data channel.
type bufferedAmounter interface {
	BufferedAmount() uint64
}

// webrtcFlushTimeout bounds how long CloseWrite waits for SCTP to drain.
const webrtcFlushTimeout = 5 * time.Second

// CloseWrite ends this side's transmission.
//
// A WebRTC data channel has no half-close: per RFC 8831 section 6.7 the peer mirrors any
// stream reset it sees, so resetting tears down both directions. Callers must therefore
// only invoke this once they are actually done sending. A side that has sent nothing
// must stay quiet, or it destroys the direction it is still waiting to receive on.
//
// The queued payload is drained before the reset so the peer reads it and *then* sees
// EOF. Close() cannot be used here: it also closes the PeerConnection, which aborts the
// SCTP association and discards data the peer has not read yet.
func (c *WebRTCConn) CloseWrite() error {
	if ba, ok := c.ReadWriteCloser.(bufferedAmounter); ok {
		deadline := time.Now().Add(webrtcFlushTimeout)
		for ba.BufferedAmount() > 0 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
	}
	return c.ReadWriteCloser.Close()
}

func (c *WebRTCConn) Close() error {
	err := c.ReadWriteCloser.Close()
	if c.pc != nil {
		c.pc.Close()
	}
	return err
}

func negotiateWebRTCClient(ctx context.Context, relayConn net.Conn) (net.Conn, error) {
	s := webrtc.SettingEngine{}
	s.DetachDataChannels()
	api := webrtc.NewAPI(webrtc.WithSettingEngine(s))

	pc, err := api.NewPeerConnection(getWebRTCConfig())
	if err != nil {
		return nil, err
	}

	dc, err := pc.CreateDataChannel("data", nil)
	if err != nil {
		pc.Close()
		return nil, err
	}

	connChan := make(chan net.Conn, 1)
	dc.OnOpen(func() {
		raw, err := dc.Detach()
		if err != nil {
			slog.Error("Failed to detach data channel", "error", err)
			return
		}
		logICEConnection(pc)
		connChan <- &WebRTCConn{ReadWriteCloser: raw, pc: pc}
	})

	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		init := c.ToJSON()
		sendSignal(relayConn, SignalMessage{Type: "candidate", Candidate: &init})
	})

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		pc.Close()
		return nil, err
	}
	if err = pc.SetLocalDescription(offer); err != nil {
		pc.Close()
		return nil, err
	}

	if err := sendSignal(relayConn, SignalMessage{Type: "offer", SDP: offer.SDP}); err != nil {
		pc.Close()
		return nil, err
	}

	resp, err := readSignal(relayConn)
	if err != nil {
		pc.Close()
		return nil, err
	}
	if resp.Type == "fallback" {
		pc.Close()
		return nil, fmt.Errorf("server requested fallback")
	}
	if resp.Type != "answer" {
		pc.Close()
		return nil, fmt.Errorf("expected answer, got %s", resp.Type)
	}

	answer := webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: resp.SDP}
	if err := pc.SetRemoteDescription(answer); err != nil {
		pc.Close()
		return nil, err
	}

	go func() {
		for {
			msg, err := readSignal(relayConn)
			if err != nil {
				return
			}
			if msg.Type == "candidate" && msg.Candidate != nil {
				pc.AddICECandidate(*msg.Candidate)
			}
		}
	}()

	select {
	case conn := <-connChan:
		return conn, nil
	case <-ctx.Done():
		pc.Close()
		return nil, ctx.Err()
	case <-time.After(15 * time.Second):
		pc.Close()
		return nil, fmt.Errorf("timeout waiting for WebRTC connection")
	}
}

func negotiateWebRTCServer(ctx context.Context, relayConn net.Conn) (net.Conn, bool, error) {
	msg, err := readSignal(relayConn)
	if err != nil {
		return nil, false, err
	}
	if msg.Type == "fallback" {
		return nil, true, nil
	}
	if msg.Type != "offer" {
		return nil, false, fmt.Errorf("expected offer, got %s", msg.Type)
	}

	s := webrtc.SettingEngine{}
	s.DetachDataChannels()
	api := webrtc.NewAPI(webrtc.WithSettingEngine(s))

	pc, err := api.NewPeerConnection(getWebRTCConfig())
	if err != nil {
		sendSignal(relayConn, SignalMessage{Type: "fallback"})
		return nil, true, err
	}

	connChan := make(chan net.Conn, 1)
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		dc.OnOpen(func() {
			raw, err := dc.Detach()
			if err != nil {
				return
			}
			logICEConnection(pc)
			connChan <- &WebRTCConn{ReadWriteCloser: raw, pc: pc}
		})
	})

	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		init := c.ToJSON()
		sendSignal(relayConn, SignalMessage{Type: "candidate", Candidate: &init})
	})

	offer := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: msg.SDP}
	if err := pc.SetRemoteDescription(offer); err != nil {
		pc.Close()
		sendSignal(relayConn, SignalMessage{Type: "fallback"})
		return nil, true, err
	}

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		pc.Close()
		sendSignal(relayConn, SignalMessage{Type: "fallback"})
		return nil, true, err
	}
	if err := pc.SetLocalDescription(answer); err != nil {
		pc.Close()
		sendSignal(relayConn, SignalMessage{Type: "fallback"})
		return nil, true, err
	}

	if err := sendSignal(relayConn, SignalMessage{Type: "answer", SDP: answer.SDP}); err != nil {
		pc.Close()
		return nil, false, err
	}

	go func() {
		for {
			msg, err := readSignal(relayConn)
			if err != nil {
				return
			}
			if msg.Type == "candidate" && msg.Candidate != nil {
				pc.AddICECandidate(*msg.Candidate)
			}
		}
	}()

	select {
	case conn := <-connChan:
		return conn, false, nil
	case <-ctx.Done():
		pc.Close()
		return nil, false, ctx.Err()
	case <-time.After(15 * time.Second):
		pc.Close()
		return nil, false, fmt.Errorf("timeout waiting for WebRTC connection")
	}
}

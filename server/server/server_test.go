package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/network-tunnel/net-tunnel/server/config"
	"github.com/network-tunnel/net-tunnel/server/protocol"
)

func newTestServer(t *testing.T) *Server {
	cfg := &config.Config{
		Listen:    "127.0.0.1:0",
		AuthToken: "test-token",
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func wsHandler(s *Server) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := s.upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		p := &peer{conn: conn}

		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				break
			}
			var msg protocol.Message
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}
			s.handleMessage(p, &msg)
		}
	})
}

func testDial(t *testing.T, srv *httptest.Server) *websocket.Conn {
	u := "ws" + srv.URL[4:] + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	return conn
}

func testReadMsg(t *testing.T, conn *websocket.Conn) *protocol.Message {
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	var msg protocol.Message
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	return &msg
}

func testSendMsg(t *testing.T, conn *websocket.Conn, msg *protocol.Message) {
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
}

func testRegister(t *testing.T, conn *websocket.Conn, name string, allow bool) protocol.RegisterRespPayload {
	testSendMsg(t, conn, &protocol.Message{
		Type: protocol.MsgTypeRegister,
		Payload: protocol.RegisterPayload{
			ClientName:   name,
			AuthToken:    "test-token",
			AllowPossess: allow,
		},
	})
	resp := testReadMsg(t, conn)
	if resp.Type != protocol.MsgTypeRegisterResp {
		t.Fatalf("expected register_resp, got %s", resp.Type)
	}
	var reg protocol.RegisterRespPayload
	data, _ := json.Marshal(resp.Payload)
	json.Unmarshal(data, &reg)
	return reg
}

func TestServerRegister(t *testing.T) {
	s := newTestServer(t)
	httpSrv := httptest.NewServer(wsHandler(s))
	defer httpSrv.Close()

	conn := testDial(t, httpSrv)
	defer conn.Close()

	reg := testRegister(t, conn, "test-client", true)

	if reg.ClientID == "" {
		t.Fatal("expected non-empty client ID")
	}
	if reg.TunnelIP == "" {
		t.Fatal("expected non-empty tunnel IP")
	}
}

func TestServerPeerDiscovery(t *testing.T) {
	s := newTestServer(t)
	httpSrv := httptest.NewServer(wsHandler(s))
	defer httpSrv.Close()

	conn1 := testDial(t, httpSrv)
	defer conn1.Close()

	conn2 := testDial(t, httpSrv)
	defer conn2.Close()

	testRegister(t, conn1, "client-a", true)
	testRegister(t, conn2, "client-b", false)

	testSendMsg(t, conn1, &protocol.Message{
		Type: protocol.MsgTypeListPeers,
	})

	resp := testReadMsg(t, conn1)
	if resp.Type != protocol.MsgTypePeerList {
		t.Fatalf("expected peer_list, got %s", resp.Type)
	}

	var peers []protocol.PeerInfo
	data, _ := json.Marshal(resp.Payload)
	json.Unmarshal(data, &peers)

	if len(peers) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(peers))
	}
	if peers[0].ClientName != "client-b" {
		t.Fatalf("expected client-b, got %s", peers[0].ClientName)
	}
}

func TestServerPossessToggle(t *testing.T) {
	s := newTestServer(t)
	httpSrv := httptest.NewServer(wsHandler(s))
	defer httpSrv.Close()

	conn1 := testDial(t, httpSrv)
	defer conn1.Close()

	conn2 := testDial(t, httpSrv)
	defer conn2.Close()

	testRegister(t, conn1, "client-a", true)
	testRegister(t, conn2, "client-b", false)

	testSendMsg(t, conn2, &protocol.Message{
		Type: protocol.MsgTypePossessToggle,
		Payload: protocol.PossessTogglePayload{
			AllowPossess: true,
		},
	})

	update := testReadMsg(t, conn1)
	if update.Type != protocol.MsgTypePossessUpdate {
		t.Fatalf("expected possess_update, got %s", update.Type)
	}

	var updatePayload protocol.PossessUpdatePayload
	data, _ := json.Marshal(update.Payload)
	json.Unmarshal(data, &updatePayload)

	if !updatePayload.AllowPossess {
		t.Fatal("expected allow_possess=true")
	}
}

func TestServerConnectRequest(t *testing.T) {
	s := newTestServer(t)
	httpSrv := httptest.NewServer(wsHandler(s))
	defer httpSrv.Close()

	conn1 := testDial(t, httpSrv)
	defer conn1.Close()

	conn2 := testDial(t, httpSrv)
	defer conn2.Close()

	reg2 := testRegister(t, conn2, "client-b", true)
	testRegister(t, conn1, "client-a", true)

	testSendMsg(t, conn1, &protocol.Message{
		Type: protocol.MsgTypeConnectRequest,
		Payload: protocol.ConnectRequestPayload{
			TargetClientID: reg2.ClientID,
		},
	})

	offer := testReadMsg(t, conn2)
	if offer.Type != protocol.MsgTypeSignalOffer {
		t.Fatalf("expected signal_offer, got %s", offer.Type)
	}

	connectResp := testReadMsg(t, conn1)
	if connectResp.Type != protocol.MsgTypeConnectResp {
		t.Fatalf("expected connect_resp, got %s", connectResp.Type)
	}
}

func TestServerConnectRequestDenied(t *testing.T) {
	s := newTestServer(t)
	httpSrv := httptest.NewServer(wsHandler(s))
	defer httpSrv.Close()

	conn1 := testDial(t, httpSrv)
	defer conn1.Close()

	conn2 := testDial(t, httpSrv)
	defer conn2.Close()

	testRegister(t, conn1, "client-a", true)
	reg2 := testRegister(t, conn2, "client-b", false)

	testSendMsg(t, conn1, &protocol.Message{
		Type: protocol.MsgTypeConnectRequest,
		Payload: protocol.ConnectRequestPayload{
			TargetClientID: reg2.ClientID,
		},
	})

	connectResp := testReadMsg(t, conn1)
	if connectResp.Type != protocol.MsgTypeConnectResp {
		t.Fatalf("expected connect_resp, got %s", connectResp.Type)
	}

	var resp protocol.ConnectRespPayload
	data, _ := json.Marshal(connectResp.Payload)
	json.Unmarshal(data, &resp)

	if resp.Success {
		t.Fatal("expected connection to be denied")
	}
}

func TestServerHeartbeat(t *testing.T) {
	s := newTestServer(t)
	httpSrv := httptest.NewServer(wsHandler(s))
	defer httpSrv.Close()

	conn := testDial(t, httpSrv)
	defer conn.Close()

	testSendMsg(t, conn, &protocol.Message{
		Type: protocol.MsgTypeHeartbeat,
	})

	resp := testReadMsg(t, conn)
	if resp.Type != protocol.MsgTypeHeartbeat {
		t.Fatalf("expected heartbeat, got %s", resp.Type)
	}
}

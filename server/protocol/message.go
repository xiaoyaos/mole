package protocol

const (
	MsgTypeRegister       = "register"
	MsgTypeRegisterResp   = "register_resp"
	MsgTypeListPeers      = "list_peers"
	MsgTypePeerList       = "peer_list"
	MsgTypeConnectRequest = "connect_request"
	MsgTypeConnectResp    = "connect_resp"
	MsgTypePossessToggle  = "possess_toggle"
	MsgTypePossessUpdate  = "possess_update"
	MsgTypeDisconnect     = "disconnect"
	MsgTypeError          = "error"
	MsgTypeSignalOffer    = "signal_offer"
	MsgTypeSignalAnswer   = "signal_answer"
	MsgTypeSignalICE      = "signal_ice"
	MsgTypeHeartbeat      = "heartbeat"
)

type Message struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload,omitempty"`
}

type RegisterPayload struct {
	ClientName   string `json:"client_name"`
	AuthToken    string `json:"auth_token,omitempty"`
	AllowPossess bool   `json:"allow_possess"`
	TunnelNet    string `json:"tunnel_net,omitempty"`
	LocalSubnet  string `json:"local_subnet,omitempty"`
}

type RegisterRespPayload struct {
	ClientID  string `json:"client_id"`
	TunnelIP  string `json:"tunnel_ip"`
	TunnelNet string `json:"tunnel_net"`
}

type PeerInfo struct {
	ClientID     string `json:"client_id"`
	ClientName   string `json:"client_name"`
	TunnelIP     string `json:"tunnel_ip"`
	AllowPossess bool   `json:"allow_possess"`
	LocalSubnet  string `json:"local_subnet,omitempty"`
	Connected    bool   `json:"connected"`
}

type ConnectRequestPayload struct {
	TargetClientID string `json:"target_client_id"`
}

type ConnectRespPayload struct {
	Success     bool   `json:"success"`
	TargetID    string `json:"target_id,omitempty"`
	TargetIP    string `json:"target_ip,omitempty"`
	TargetName  string `json:"target_name,omitempty"`
	LocalSubnet string `json:"local_subnet,omitempty"`
	Message     string `json:"message,omitempty"`
}

type PossessTogglePayload struct {
	AllowPossess bool `json:"allow_possess"`
}

type PossessUpdatePayload struct {
	ClientID     string `json:"client_id"`
	AllowPossess bool   `json:"allow_possess"`
}

type SignalPayload struct {
	TargetClientID string `json:"target_client_id"`
	FromClientID   string `json:"from_client_id"`
	Data           string `json:"data"`
}

type ErrorPayload struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

package client

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"mole/pkg/config"
	"mole/pkg/protocol"
	"mole/pkg/tunnel"
)

func splitSubnets(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

var privateParents = []struct {
	ip   net.IP
	mask net.IPMask
}{
	{net.IPv4(10, 0, 0, 0), net.CIDRMask(8, 32)},
	{net.IPv4(172, 16, 0, 0), net.CIDRMask(12, 32)},
	{net.IPv4(192, 168, 0, 0), net.CIDRMask(16, 32)},
}

// expandPeerSubnets returns the peer's subnets plus broader RFC 1918 parents.
// This lets the receiving side route all private traffic that may be
// reachable through the peer's default gateway.
func expandPeerSubnets(subnetCSV string) []string {
	subnets := splitSubnets(subnetCSV)
	seen := map[string]bool{}
	for _, s := range subnets {
		seen[s] = true
	}

	for _, s := range subnets {
		_, cidr, err := net.ParseCIDR(s)
		if err != nil {
			continue
		}
		for _, p := range privateParents {
			n := &net.IPNet{IP: p.ip, Mask: p.mask}
			if n.Contains(cidr.IP) {
				parentCIDR := n.String()
				if !seen[parentCIDR] {
					seen[parentCIDR] = true
					subnets = append(subnets, parentCIDR)
				}
			}
		}
	}
	return subnets
}

type peerConnection struct {
	mu           sync.Mutex
	peerID       string
	peerName     string
	peerIP       string
	allowPossess bool
	localSubnet  string
	conn         net.Conn
	cancel       chan struct{}
}

type Client struct {
	cfg           *config.ClientConfig
	wsConn        *websocket.Conn
	clientID      string
	tunnelIP      string
	tunIface      *tunnel.Interface
	peers         map[string]*peerConnection
	mu            sync.RWMutex
	wsMu          sync.Mutex
	relayAddr     string
	localIface    string
	stopCh        chan struct{}
	stoppedCh     chan struct{}
	stopped       bool
	peerListCache []protocol.PeerInfo
	peerCacheMu   sync.RWMutex
}

func NewClient(cfg *config.ClientConfig) *Client {
	return &Client{
		cfg:       cfg,
		peers:     make(map[string]*peerConnection),
		relayAddr: cfg.ServerAddr,
		stopCh:    make(chan struct{}),
		stoppedCh: make(chan struct{}),
	}
}

func (c *Client) Start() error {
	for {
		err := c.run()
		if err != nil {
			log.Printf("连接断开: %v，5秒后重连...", err)
		} else {
			return nil
		}
		select {
		case <-c.stoppedCh:
			return nil
		case <-time.After(5 * time.Second):
		}
	}
}

func (c *Client) run() error {
	c.mu.Lock()
	c.stopped = false
	c.peers = make(map[string]*peerConnection)
	c.mu.Unlock()
	c.stopCh = make(chan struct{})
	defer c.cleanup()

	if err := c.connectWebSocket(); err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	if err := c.register(); err != nil {
		return fmt.Errorf("register: %w", err)
	}

	tun, err := tunnel.CreateTUN(c.tunnelIP, c.cfg.TunnelNet, 1500)
	if err != nil {
		return fmt.Errorf("create TUN: %w", err)
	}
	c.tunIface = tun
	log.Printf("TUN interface %s created with IP %s", tun.Name(), c.tunnelIP)

	_, ipnet, _ := net.ParseCIDR(c.cfg.TunnelNet)
	if ipnet != nil {
		ones, _ := ipnet.Mask.Size()
		log.Printf("Tunnel network: %s/%d, your IP: %s", ipnet.IP.String(), ones, c.tunnelIP)
	}

	if c.cfg.LocalSubnet != "" {
		c.setupLocalSubnet(tun)
	} else {
		log.Printf("No local subnet specified, trying auto-detect...")
		if subnet, iface, err := tunnel.AutoDetectSubnet(); err == nil {
			c.cfg.LocalSubnet = subnet
			c.localIface = iface
			log.Printf("Auto-detected subnet: %s (interface: %s)", subnet, iface)
			c.setupLocalSubnet(tun)
		} else {
			log.Printf("Warning: auto-detect subnet: %v", err)
			log.Printf("Use -local-subnet to specify manually")
		}
	}

	go c.tunForwarder()

	go c.heartbeat()

	c.messageLoop()

	return fmt.Errorf("connection closed")
}

func (c *Client) setupLocalSubnet(tun *tunnel.Interface) {
	subnets := splitSubnets(c.cfg.LocalSubnet)
	log.Printf("Sharing local subnets: %v", subnets)
	if err := tunnel.EnableIPForward(); err != nil {
		log.Printf("Warning: enable IP forward: %v", err)
	}
	if c.localIface == "" {
		for _, s := range subnets {
			if iface, err := tunnel.FindInterfaceForSubnet(s); err == nil {
				c.localIface = iface
				log.Printf("Found local interface %s for subnet %s", iface, s)
				break
			}
		}
		if c.localIface == "" {
			log.Printf("Warning: could not find local interface for any subnet, SNAT may not work")
		}
	}
	if runtime.GOOS == "windows" || c.localIface != "" {
		if err := tun.AddNAT(c.localIface); err != nil {
			log.Printf("Warning: add NAT: %v", err)
		}
	}
}

func (c *Client) connectWebSocket() error {
	u := fmt.Sprintf("ws://%s/ws", c.cfg.ServerAddr)
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		return fmt.Errorf("dial websocket: %w", err)
	}
	c.wsConn = conn
	log.Printf("Connected to relay server at %s", c.cfg.ServerAddr)
	return nil
}

func (c *Client) register() error {
	payload := protocol.RegisterPayload{
		ClientName:   c.cfg.ClientName,
		AuthToken:    c.cfg.AuthToken,
		AllowPossess: c.cfg.AllowPossess,
		TunnelNet:    c.cfg.TunnelNet,
		LocalSubnet:  c.cfg.LocalSubnet,
	}

	msg := protocol.Message{
		Type:    protocol.MsgTypeRegister,
		Payload: payload,
	}

	if err := c.sendMessage(&msg); err != nil {
		return fmt.Errorf("send register: %w", err)
	}

	_, data, err := c.wsConn.ReadMessage()
	if err != nil {
		return fmt.Errorf("read register resp: %w", err)
	}

	var resp protocol.Message
	if err := json.Unmarshal(data, &resp); err != nil {
		return fmt.Errorf("parse register resp: %w", err)
	}

	if resp.Type == protocol.MsgTypeError {
		var errPayload protocol.ErrorPayload
		if err := marshalPayload(resp.Payload, &errPayload); err == nil {
			return fmt.Errorf("register error: %s", errPayload.Message)
		}
		return fmt.Errorf("register error")
	}

	if resp.Type != protocol.MsgTypeRegisterResp {
		return fmt.Errorf("unexpected response: %s", resp.Type)
	}

	var regResp protocol.RegisterRespPayload
	if err := marshalPayload(resp.Payload, &regResp); err != nil {
		return fmt.Errorf("parse reg resp: %w", err)
	}

	c.clientID = regResp.ClientID
	c.tunnelIP = regResp.TunnelIP
	log.Printf("Registered as %s with tunnel IP %s", c.clientID, c.tunnelIP)
	return nil
}

func (c *Client) messageLoop() {
	defer c.wsConn.Close()

	for {
		_, data, err := c.wsConn.ReadMessage()
		if err != nil {
			log.Printf("Read error: %v", err)
			break
		}

		var msg protocol.Message
		if err := json.Unmarshal(data, &msg); err != nil {
			log.Printf("Parse error: %v", err)
			continue
		}

		c.handleMessage(&msg)
	}
}

func (c *Client) handleMessage(msg *protocol.Message) {
	switch msg.Type {
	case protocol.MsgTypePeerList:
		c.handlePeerList(msg)
	case protocol.MsgTypeConnectResp:
		c.handleConnectResp(msg)
	case protocol.MsgTypeSignalOffer:
		c.handleSignalOffer(msg)
	case protocol.MsgTypeSignalAnswer:
		c.handleSignalAnswer(msg)
	case protocol.MsgTypeSignalICE:
		c.handleSignalICE(msg)
	case protocol.MsgTypePossessUpdate:
		c.handlePossessUpdate(msg)
	case protocol.MsgTypeError:
		c.handleError(msg)
	case protocol.MsgTypeHeartbeat:
	case protocol.MsgTypeRegisterResp:
	default:
		log.Printf("Unknown message type: %s", msg.Type)
	}
}

func (c *Client) handlePeerList(msg *protocol.Message) {
	var peers []protocol.PeerInfo
	if err := marshalPayload(msg.Payload, &peers); err != nil {
		log.Printf("Parse peer list error: %v", err)
		return
	}

	c.peerCacheMu.Lock()
	c.peerListCache = peers
	c.peerCacheMu.Unlock()

	log.Printf("Available peers (%d):", len(peers))
	for i, p := range peers {
		possessStr := "allow"
		if !p.AllowPossess {
			possessStr = "deny"
		}
		subnetInfo := ""
		if p.LocalSubnet != "" {
			subnetInfo = fmt.Sprintf(", subnets=%s", p.LocalSubnet)
		}
		log.Printf("  %d. %s (%s) IP=%s [possess=%s]%s",
			i+1, p.ClientName, shortID(p.ClientID), p.TunnelIP, possessStr, subnetInfo)
	}
}

func (c *Client) handleConnectResp(msg *protocol.Message) {
	var resp protocol.ConnectRespPayload
	if err := marshalPayload(msg.Payload, &resp); err != nil {
		log.Printf("Parse connect resp error: %v", err)
		return
	}

	if resp.Success {
		log.Printf("Connect request sent to %s (%s)", resp.TargetName, resp.TargetIP)
		c.establishDataConnection(resp.TargetID, resp.TargetIP, resp.TargetName, resp.LocalSubnet)
	} else {
		log.Printf("Connect failed: %s", resp.Message)
	}
}

func (c *Client) handleSignalOffer(msg *protocol.Message) {
	var signal protocol.SignalPayload
	if err := marshalPayload(msg.Payload, &signal); err != nil {
		log.Printf("Parse signal error: %v", err)
		return
	}

	log.Printf("Connection request from %s (%s)", signal.FromClientID[:8], signal.FromTunnelIP)

	if !c.cfg.AllowPossess {
		log.Printf("Rejecting connection (possession denied)")
		return
	}

	c.establishDataConnection(signal.FromClientID, signal.FromTunnelIP, "", "")
}

func (c *Client) handleSignalAnswer(msg *protocol.Message) {
}

func (c *Client) handleSignalICE(msg *protocol.Message) {
}

func (c *Client) handlePossessUpdate(msg *protocol.Message) {
	var update protocol.PossessUpdatePayload
	if err := marshalPayload(msg.Payload, &update); err != nil {
		return
	}

	c.mu.RLock()
	peer, exists := c.peers[update.ClientID]
	c.mu.RUnlock()

	if exists {
		peer.mu.Lock()
		peer.allowPossess = update.AllowPossess
		peer.mu.Unlock()

		if !update.AllowPossess {
			log.Printf("Peer %s revoked possession, disconnecting", update.ClientID[:8])
			go c.disconnectPeer(update.ClientID)
		}
	}
}

func (c *Client) handleError(msg *protocol.Message) {
	var errPayload protocol.ErrorPayload
	if err := marshalPayload(msg.Payload, &errPayload); err == nil {
		log.Printf("Server error [%d]: %s", errPayload.Code, errPayload.Message)
	}
}

func (c *Client) establishDataConnection(peerID, peerIP, peerName, localSubnet string) {
	c.mu.Lock()
	if _, exists := c.peers[peerID]; exists {
		c.mu.Unlock()
		log.Printf("Already connected to %s", peerID[:8])
		return
	}

	pc := &peerConnection{
		peerID:      peerID,
		peerName:    peerName,
		peerIP:      peerIP,
		localSubnet: localSubnet,
		cancel:      make(chan struct{}),
	}
	c.peers[peerID] = pc
	c.mu.Unlock()

	go c.connectToPeer(pc)
}

func (c *Client) connectToPeer(pc *peerConnection) {
	relayHost, _, err := net.SplitHostPort(c.relayAddr)
	if err != nil {
		log.Printf("Invalid relay addr: %v", err)
		c.removePeer(pc.peerID)
		return
	}

	relayAddr := fmt.Sprintf("%s:8081", relayHost)
	a, b := c.clientID, pc.peerID
	if a > b {
		a, b = b, a
	}
	sessionID := fmt.Sprintf("%s-%s", a, b)

	log.Printf("Connecting to peer %s via relay at %s", pc.peerID[:8], relayAddr)

	conn, err := net.DialTimeout("tcp", relayAddr, 10*time.Second)
	if err != nil {
		log.Printf("Relay connect error: %v", err)
		c.removePeer(pc.peerID)
		return
	}
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetKeepAlive(true)
		tcpConn.SetKeepAlivePeriod(15 * time.Second)
	}

	handshake := fmt.Sprintf("RELAY %s\n", sessionID)
	if _, err := conn.Write([]byte(handshake)); err != nil {
		log.Printf("Relay handshake error: %v", err)
		conn.Close()
		c.removePeer(pc.peerID)
		return
	}

	response := make([]byte, 3)
	if _, err := io.ReadFull(conn, response); err != nil || string(response) != "OK\n" {
		log.Printf("Relay handshake response error: %v", err)
		conn.Close()
		c.removePeer(pc.peerID)
		return
	}

	pc.conn = conn
	log.Printf("Relay connection established with %s", pc.peerID[:8])

	if pc.localSubnet != "" && c.tunIface != nil {
		for _, subnet := range expandPeerSubnets(pc.localSubnet) {
			if err := c.tunIface.AddRoute(subnet); err != nil {
				log.Printf("Add route for %s: %v", subnet, err)
			} else {
				log.Printf("Route added: %s -> %s", subnet, c.tunIface.Name())
			}
		}
	}

	go c.peerReader(pc)
}

func (c *Client) peerReader(pc *peerConnection) {
	defer func() {
		c.removePeer(pc.peerID)
	}()

	for {
		var length uint16
		if err := binary.Read(pc.conn, binary.BigEndian, &length); err != nil {
			select {
			case <-pc.cancel:
				return
			default:
				log.Printf("Peer read error from %s: %v", pc.peerID[:8], err)
				return
			}
		}

		data := make([]byte, length)
		if _, err := io.ReadFull(pc.conn, data); err != nil {
			log.Printf("Peer data read error from %s: %v", pc.peerID[:8], err)
			return
		}

		if err := c.tunIface.WritePacket(data); err != nil {
			log.Printf("TUN write error: %v", err)
		}
	}
}

func (c *Client) tunForwarder() {
	for {
		packet, err := c.tunIface.ReadPacket()
		if err != nil {
			select {
			case <-c.stopCh:
				return
			default:
				log.Printf("TUN read error: %v", err)
				time.Sleep(100 * time.Millisecond)
				continue
			}
		}

		if len(packet) < 20 {
			continue
		}

		version := packet[0] >> 4
		if version != 4 {
			continue
		}

		destIP := net.IP(packet[16:20])
		if destIP.IsMulticast() || destIP.Equal(net.IPv4(255, 255, 255, 255)) || destIP.IsLoopback() || destIP[0] == 127 {
			continue
		}

		// Strip TCP timestamp option from forwarded SYNs to avoid macOS fingerprinting
		if len(packet) > 20 && packet[9] == 6 {
			ipHdrLen := int(packet[0]&0x0F) * 4
			if len(packet) >= ipHdrLen+14 {
				flags := packet[ipHdrLen+13]
				if (flags&0x02) != 0 && (flags&0x10) == 0 {
					packet = stripTCPTimestamp(packet)
				}
			}
		}

		if peer := c.findPeerForDest(destIP); peer != nil {
			c.sendToPeer(peer, packet)
		}
	}
}

func (c *Client) findPeerForDest(destIP net.IP) *peerConnection {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, pc := range c.peers {
		if pc.peerIP != "" {
			peerIP := net.ParseIP(pc.peerIP)
			if peerIP != nil && destIP.Equal(peerIP) {
				return pc
			}
		}

		if pc.localSubnet != "" {
			for _, s := range splitSubnets(pc.localSubnet) {
				_, subnet, err := net.ParseCIDR(s)
				if err == nil && subnet.Contains(destIP) {
					return pc
				}
			}
		}
	}
	return nil
}

func (c *Client) sendToPeer(pc *peerConnection, data []byte) error {
	if len(data) > 65535 {
		return fmt.Errorf("packet too large: %d", len(data))
	}
	pc.mu.Lock()
	defer pc.mu.Unlock()

	if pc.conn == nil {
		return fmt.Errorf("peer not connected")
	}

	pc.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	defer pc.conn.SetWriteDeadline(time.Time{})

	length := uint16(len(data))
	if err := binary.Write(pc.conn, binary.BigEndian, length); err != nil {
		return err
	}
	_, err := pc.conn.Write(data)
	return err
}

func (c *Client) disconnectPeer(peerID string) {
	c.mu.RLock()
	pc, exists := c.peers[peerID]
	c.mu.RUnlock()

	if exists {
		close(pc.cancel)
		pc.mu.Lock()
		if pc.conn != nil {
			pc.conn.Close()
		}
		pc.mu.Unlock()
		c.removePeer(peerID)
	}
}

func (c *Client) removePeer(peerID string) {
	c.mu.Lock()

	if pc, exists := c.peers[peerID]; exists {
		if pc.localSubnet != "" && c.tunIface != nil {
			for _, subnet := range expandPeerSubnets(pc.localSubnet) {
				if err := c.tunIface.RemoveRoute(subnet); err != nil {
					log.Printf("Remove route %s: %v", subnet, err)
				}
			}
		}

		pc.mu.Lock()
		if pc.conn != nil {
			pc.conn.Close()
		}
		pc.mu.Unlock()
		delete(c.peers, peerID)
		log.Printf("Disconnected from %s", peerID[:8])
	}
	c.mu.Unlock()
}

func (c *Client) cleanup() {
	c.mu.Lock()
	closeStopCh := !c.stopped
	c.stopped = true
	c.mu.Unlock()

	if c.tunIface != nil {
		c.mu.RLock()
		peers := make([]*peerConnection, 0, len(c.peers))
		for _, pc := range c.peers {
			peers = append(peers, pc)
		}
		c.mu.RUnlock()

		for _, pc := range peers {
			if pc.localSubnet != "" {
				for _, subnet := range expandPeerSubnets(pc.localSubnet) {
					c.tunIface.RemoveRoute(subnet)
				}
			}
			pc.mu.Lock()
			if pc.conn != nil {
				pc.conn.Close()
			}
			pc.mu.Unlock()
		}
		if c.localIface != "" {
			c.tunIface.RemoveNAT(c.localIface)
		}
		c.tunIface.Close()
	}

	if closeStopCh {
		select {
		case <-c.stopCh:
		default:
			close(c.stopCh)
		}
	}
}

func (c *Client) Stop() {
	c.mu.Lock()
	alreadyStopped := c.stopped
	c.stopped = true
	c.mu.Unlock()

	select {
	case <-c.stoppedCh:
	default:
		close(c.stoppedCh)
	}

	if !alreadyStopped {
		if c.wsConn != nil {
			c.wsConn.Close()
		}
		select {
		case <-c.stopCh:
		default:
			close(c.stopCh)
		}
	}
}

func (c *Client) heartbeat() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			msg := protocol.Message{Type: protocol.MsgTypeHeartbeat}
			if err := c.sendMessage(&msg); err != nil {
				log.Printf("Heartbeat error: %v", err)
			}
		case <-c.stopCh:
			return
		}
	}
}

func (c *Client) ListPeers() error {
	msg := protocol.Message{Type: protocol.MsgTypeListPeers}
	return c.sendMessage(&msg)
}

func (c *Client) ConnectTo(targetClientID string) error {
	msg := protocol.Message{
		Type: protocol.MsgTypeConnectRequest,
		Payload: protocol.ConnectRequestPayload{
			TargetClientID: targetClientID,
		},
	}
	return c.sendMessage(&msg)
}

func (c *Client) TogglePossess(allow bool) error {
	c.cfg.AllowPossess = allow
	msg := protocol.Message{
		Type: protocol.MsgTypePossessToggle,
		Payload: protocol.PossessTogglePayload{
			AllowPossess: allow,
		},
	}
	return c.sendMessage(&msg)
}

func (c *Client) AllowPossess() bool {
	return c.cfg.AllowPossess
}

func (c *Client) TunnelIP() string {
	return c.tunnelIP
}

func (c *Client) ClientID() string {
	return c.clientID
}

func (c *Client) Peers() map[string]*peerConnection {
	c.mu.RLock()
	defer c.mu.RUnlock()
	peers := make(map[string]*peerConnection, len(c.peers))
	for k, v := range c.peers {
		peers[k] = v
	}
	return peers
}

func (p *peerConnection) PeerID() string   { return p.peerID }
func (p *peerConnection) PeerName() string { return p.peerName }
func (p *peerConnection) PeerIP() string   { return p.peerIP }
func (p *peerConnection) AllowPossess() bool { return p.allowPossess }
func (p *peerConnection) LocalSubnet() string { return p.localSubnet }

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func (c *Client) ConnectByIndex(idx int) error {
	c.peerCacheMu.RLock()
	defer c.peerCacheMu.RUnlock()
	if idx < 1 || idx > len(c.peerListCache) {
		return fmt.Errorf("index out of range: have %d peers, got %d", len(c.peerListCache), idx)
	}
	return c.ConnectTo(c.peerListCache[idx-1].ClientID)
}

func (c *Client) DisconnectPeer(peerID string) {
	c.disconnectPeer(peerID)
}

func (c *Client) DisconnectByIndex(idx int) error {
	c.peerCacheMu.RLock()
	peers := c.peerListCache
	c.peerCacheMu.RUnlock()

	if idx < 1 || idx > len(peers) {
		return fmt.Errorf("index out of range: have %d peers, got %d", len(peers), idx)
	}
	c.DisconnectPeer(peers[idx-1].ClientID)
	return nil
}

func (c *Client) sendMessage(msg *protocol.Message) error {
	c.wsMu.Lock()
	defer c.wsMu.Unlock()

	if c.wsConn == nil {
		return fmt.Errorf("not connected")
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return c.wsConn.WriteMessage(websocket.TextMessage, data)
}

func marshalPayload(from, to interface{}) error {
	data, err := json.Marshal(from)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, to)
}

// stripTCPTimestamp removes TCP timestamp option (type 8) from SYN packets
// to avoid OS fingerprinting — macOS randomizes timestamps, Windows doesn't.
func stripTCPTimestamp(packet []byte) []byte {
	ipHdrLen := int(packet[0]&0x0F) * 4
	if len(packet) < ipHdrLen+20 {
		return packet
	}
	tcpOff := ipHdrLen
	dataOff := int(packet[tcpOff+12] >> 4) * 4
	if dataOff <= 20 {
		return packet
	}

	optEnd := tcpOff + dataOff
	optStart := tcpOff + 20

	// Scan for timestamp option (kind=8)
	stripped := false
	buf := make([]byte, 0, dataOff-20)
	for i := optStart; i < optEnd; {
		if i >= len(packet) {
			break
		}
		kind := packet[i]
		if kind == 0 {
			break
		}
		if kind == 1 {
			buf = append(buf, 1)
			i++
			continue
		}
		if i+1 >= len(packet) {
			break
		}
		length := int(packet[i+1])
		if length < 2 || i+length > optEnd || i+length > len(packet) {
			break
		}
		if kind == 8 && length == 10 {
			stripped = true
			i += length
			continue
		}
		buf = append(buf, packet[i:i+length]...)
		i += length
	}

	if !stripped {
		return packet
	}

	// Pad to 4-byte boundary
	for len(buf)%4 != 0 {
		buf = append(buf, 1)
	}

	newDataOff := 20 + len(buf)
	if newDataOff > 60 {
		return packet
	}

	// Build modified packet
	newLen := tcpOff + newDataOff + (len(packet) - (tcpOff + dataOff))
	out := make([]byte, newLen)
	copy(out, packet[:tcpOff+20])
	copy(out[tcpOff+20:tcpOff+20+len(buf)], buf)
	copy(out[tcpOff+newDataOff:], packet[tcpOff+dataOff:])

	// Update TCP data offset
	out[tcpOff+12] = (out[tcpOff+12] & 0x0F) | byte((newDataOff/4)<<4)

	// Update IP Total Length
	totalLen := uint16(newLen)
	out[2] = byte(totalLen >> 8)
	out[3] = byte(totalLen)

	// Recalculate IP header checksum
	setIPChecksum(out[:tcpOff])

	// Recalculate TCP checksum
	srcIP := packet[12:16]
	dstIP := packet[16:20]
	tcpLen := newDataOff + (len(packet) - (tcpOff + dataOff))
	setTCPChecksum(out[tcpOff:tcpOff+tcpLen], srcIP, dstIP)

	return out
}

func setIPChecksum(ipHeader []byte) {
	ipHeader[10] = 0
	ipHeader[11] = 0

	var sum uint32
	for i := 0; i < len(ipHeader); i += 2 {
		if i+1 < len(ipHeader) {
			sum += uint32(ipHeader[i])<<8 | uint32(ipHeader[i+1])
		} else {
			sum += uint32(ipHeader[i]) << 8
		}
	}
	for (sum >> 16) > 0 {
		sum = (sum & 0xFFFF) + (sum >> 16)
	}
	cksum := uint16(^sum & 0xFFFF)
	ipHeader[10] = byte(cksum >> 8)
	ipHeader[11] = byte(cksum)
}

func setTCPChecksum(tcp []byte, srcIP, dstIP []byte) {
	tcp[16] = 0
	tcp[17] = 0

	totalLen := len(tcp)
	pseudo := make([]byte, 12+totalLen)
	copy(pseudo[0:4], srcIP)
	copy(pseudo[4:8], dstIP)
	pseudo[8] = 0
	pseudo[9] = 6
	pseudo[10] = byte(totalLen >> 8)
	pseudo[11] = byte(totalLen)
	copy(pseudo[12:], tcp)

	var sum uint32
	for i := 0; i < len(pseudo); i += 2 {
		if i+1 < len(pseudo) {
			sum += uint32(pseudo[i])<<8 | uint32(pseudo[i+1])
		} else {
			sum += uint32(pseudo[i]) << 8
		}
	}
	for (sum >> 16) > 0 {
		sum = (sum & 0xFFFF) + (sum >> 16)
	}
	cksum := uint16(^sum & 0xFFFF)
	tcp[16] = byte(cksum >> 8)
	tcp[17] = byte(cksum)
}

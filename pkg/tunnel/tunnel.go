package tunnel

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/songgao/water"
)

const (
	ethernetHeaderLen = 14
	ethTypeIP         = 0x0800
)

type Interface struct {
	*water.Interface
	name    string
	tunIP   string
	tunNet  string
	mtu     int
	isTAP   bool
	macAddr net.HardwareAddr
}

func CreateTUN(ip, cidr string, mtu int) (*Interface, error) {
	isTAP := runtime.GOOS == "windows"

	cfg := water.Config{
		DeviceType: water.TUN,
	}
	if isTAP {
		cfg.DeviceType = water.TAP
	}

	iface, err := water.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("create device: %w", err)
	}

	tun := &Interface{
		Interface: iface,
		name:      iface.Name(),
		tunIP:     ip,
		tunNet:    cidr,
		mtu:       mtu,
		isTAP:     isTAP,
	}

	if err := tun.configure(ip, cidr, mtu); err != nil {
		tun.Close()
		return nil, fmt.Errorf("configure device: %w", err)
	}

	return tun, nil
}

func (t *Interface) configure(ip, cidr string, mtu int) error {
	switch runtime.GOOS {
	case "darwin":
		return t.configureMacOS(ip, cidr, mtu)
	case "linux":
		return t.configureLinux(ip, cidr, mtu)
	case "windows":
		return t.configureWindows(ip, cidr, mtu)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

func (t *Interface) configureMacOS(ip, cidr string, mtu int) error {
	cmds := [][]string{
		{"ifconfig", t.name, "inet", ip, ip, "up"},
		{"ifconfig", t.name, "mtu", fmt.Sprintf("%d", mtu)},
		{"route", "-n", "add", "-net", cidr, "-interface", t.name},
	}
	for _, cmd := range cmds {
		if err := exec.Command(cmd[0], cmd[1:]...).Run(); err != nil {
			return fmt.Errorf("run %v: %w", cmd, err)
		}
	}
	return nil
}

func (t *Interface) configureLinux(ip, cidr string, mtu int) error {
	cmds := [][]string{
		{"ip", "addr", "add", ip + "/24", "dev", t.name},
		{"ip", "link", "set", "dev", t.name, "mtu", fmt.Sprintf("%d", mtu)},
		{"ip", "link", "set", "dev", t.name, "up"},
		{"ip", "route", "add", cidr, "dev", t.name},
	}
	for _, cmd := range cmds {
		if err := exec.Command(cmd[0], cmd[1:]...).Run(); err != nil {
			return fmt.Errorf("run %v: %w", cmd, err)
		}
	}
	return nil
}

func (t *Interface) configureWindows(ip, cidr string, mtu int) error {
	_, ipnet, _ := net.ParseCIDR(cidr)
	mask := "255.255.255.0"
	if ipnet != nil {
		mask = net.IP(ipnet.Mask).String()
	}

	exec.Command("powershell", "-ExecutionPolicy", "Bypass", "-Command",
		fmt.Sprintf("Enable-NetAdapter -Name '%s' -Confirm:$false -ErrorAction:SilentlyContinue", t.name)).Run()

	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			time.Sleep(2 * time.Second)
		}

		out, err := exec.Command("powershell", "-ExecutionPolicy", "Bypass", "-Command",
			fmt.Sprintf("Remove-NetIPAddress -InterfaceAlias '%s' -Confirm:$false -ErrorAction:SilentlyContinue; New-NetIPAddress -InterfaceAlias '%s' -IPAddress '%s' -PrefixLength 24 -ErrorAction:Stop",
				t.name, t.name, ip)).CombinedOutput()
		if err == nil {
			lastErr = nil
			break
		}
		lastErr = fmt.Errorf("attempt %d failed: %v\noutput: %s", attempt+1, err, string(out))
	}

	if lastErr != nil {
		log.Printf("PowerShell failed, trying netsh: %v", lastErr)
		out, err := exec.Command("netsh", "interface", "ipv4", "set", "address",
			fmt.Sprintf("name=%s", t.name), "source=static", fmt.Sprintf("address=%s", ip),
			fmt.Sprintf("mask=%s", mask)).CombinedOutput()
		if err != nil {
			log.Printf("netsh ipv4 output: %s", string(out))
			return fmt.Errorf("all IP config methods failed: last PowerShell: %v\nnetsh: %v: %s", lastErr, err, string(out))
		}
	}

	exec.Command("powershell", "-ExecutionPolicy", "Bypass", "-Command",
		fmt.Sprintf("Set-NetAdapterAdvancedProperty -Name '%s' -RegistryKeyword 'MTU' -RegistryValue %d -ErrorAction:SilentlyContinue", t.name, mtu)).Run()

	exec.Command("powershell", "-ExecutionPolicy", "Bypass", "-Command",
		fmt.Sprintf("Set-NetIPInterface -InterfaceAlias '%s' -Forwarding Enabled -ErrorAction:SilentlyContinue", t.name)).Run()

	exec.Command("route", "add", ipnet.IP.String(), "mask", mask, ip, "metric", "1").Run()
	return nil
}

func (t *Interface) AddRoute(subnet string) error {
	_, ipnet, err := net.ParseCIDR(subnet)
	if err != nil {
		return fmt.Errorf("parse subnet: %w", err)
	}
	mask := net.IP(ipnet.Mask).String()

	switch runtime.GOOS {
	case "darwin":
		return exec.Command("route", "-n", "add", "-net", subnet, "-interface", t.name).Run()
	case "linux":
		return exec.Command("ip", "route", "add", subnet, "dev", t.name).Run()
	case "windows":
		return exec.Command("route", "add", ipnet.IP.String(), "mask", mask, t.tunIP, "metric", "1").Run()
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

func (t *Interface) RemoveRoute(subnet string) error {
	_, ipnet, err := net.ParseCIDR(subnet)
	if err != nil {
		return fmt.Errorf("parse subnet: %w", err)
	}

	switch runtime.GOOS {
	case "darwin":
		return exec.Command("route", "-n", "delete", "-net", subnet, "-interface", t.name).Run()
	case "linux":
		return exec.Command("ip", "route", "del", subnet, "dev", t.name).Run()
	case "windows":
		return exec.Command("route", "delete", ipnet.IP.String()).Run()
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

func (t *Interface) Name() string {
	return t.name
}

func (t *Interface) ReadPacket() ([]byte, error) {
	for {
		buf := make([]byte, t.mtu+200)
		n, err := t.Read(buf)
		if err != nil {
			return nil, err
		}
		data := buf[:n]

		if !t.isTAP {
			return data, nil
		}

		if len(data) < ethernetHeaderLen {
			continue
		}
		ethType := binary.BigEndian.Uint16(data[12:14])

		if ethType == ethTypeIP {
			return data[ethernetHeaderLen:], nil
		}

		if ethType == 0x0806 && len(data) >= 42 {
			t.handleARP(data)
			continue
		}
	}
}

func (t *Interface) WritePacket(data []byte) error {
	if t.isTAP {
		frame := t.wrapEthernet(data)
		_, err := t.Write(frame)
		return err
	}
	_, err := t.Write(data)
	return err
}

func (t *Interface) extractIPFromEthernet(frame []byte) ([]byte, error) {
	if len(frame) < ethernetHeaderLen {
		return nil, fmt.Errorf("frame too short: %d", len(frame))
	}
	ethType := binary.BigEndian.Uint16(frame[12:14])
	if ethType != ethTypeIP {
		return nil, fmt.Errorf("non-IP ethertype: 0x%04x", ethType)
	}
	return frame[ethernetHeaderLen:], nil
}

func (t *Interface) handleARP(frame []byte) {
	if len(frame) < 42 {
		return
	}
	opcode := binary.BigEndian.Uint16(frame[20:22])
	if opcode != 1 {
		return
	}
	targetIP := net.IP(frame[38:42])
	myIP := net.ParseIP(t.tunIP)
	if targetIP.Equal(myIP) || targetIP == nil {
		return
	}
	if t.macAddr == nil {
		iface, err := net.InterfaceByName(t.name)
		if err == nil {
			t.macAddr = iface.HardwareAddr
		}
	}
	if t.macAddr == nil || len(t.macAddr) != 6 {
		return
	}

	reply := make([]byte, 42)
	copy(reply[0:6], frame[6:12])
	copy(reply[6:12], t.macAddr)
	binary.BigEndian.PutUint16(reply[12:14], 0x0806)
	binary.BigEndian.PutUint16(reply[14:16], 1)
	binary.BigEndian.PutUint16(reply[16:18], 0x0800)
	reply[18] = 6
	reply[19] = 4
	binary.BigEndian.PutUint16(reply[20:22], 2)
	copy(reply[22:28], t.macAddr)
	copy(reply[28:32], targetIP)
	copy(reply[32:38], frame[6:12])
	copy(reply[38:42], frame[28:32])
	t.Write(reply)
}

func (t *Interface) wrapEthernet(ipPacket []byte) []byte {
	frame := make([]byte, ethernetHeaderLen+len(ipPacket))

	if t.macAddr == nil {
		iface, err := net.InterfaceByName(t.name)
		if err == nil {
			t.macAddr = iface.HardwareAddr
		}
	}

	if t.macAddr != nil && len(t.macAddr) == 6 {
		for i := range frame[:6] {
			frame[i] = 0xff
		}
		copy(frame[6:12], t.macAddr)
	} else {
		for i := range frame[:6] {
			frame[i] = 0xff
		}
		frame[11] = 0x01
	}

	binary.BigEndian.PutUint16(frame[12:14], ethTypeIP)
	copy(frame[ethernetHeaderLen:], ipPacket)
	return frame
}

func EnableIPForward() error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("sysctl", "-w", "net.inet.ip.forwarding=1").Run()
	case "linux":
		return exec.Command("sysctl", "-w", "net.ipv4.ip_forward=1").Run()
	case "windows":
		return exec.Command("reg", "add", `HKLM\SYSTEM\CurrentControlSet\Services\Tcpip\Parameters`,
			"/v", "IPEnableRouter", "/t", "REG_DWORD", "/d", "1", "/f").Run()
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

func (t *Interface) AddNAT(localInterface string) error {
	switch runtime.GOOS {
	case "darwin":
		return t.addNATMacOS(localInterface)
	case "linux":
		return t.addNATLinux(localInterface)
	case "windows":
		return t.addNATWindows(localInterface)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

func (t *Interface) RemoveNAT(localInterface string) error {
	switch runtime.GOOS {
	case "darwin":
		return t.removeNATMacOS()
	case "linux":
		return t.removeNATLinux(localInterface)
	case "windows":
		return t.removeNATWindows(localInterface)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

func (t *Interface) addNATMacOS(localInterface string) error {
	out, err := exec.Command("pfctl", "-s", "nat").Output()
	if err != nil {
		return fmt.Errorf("check pfctl: %w", err)
	}

	anchorName := "net-tunnel"
	natRule := fmt.Sprintf("nat on %s from %s to any -> (%s)\n", localInterface, t.tunNet, localInterface)

	cmd := fmt.Sprintf("echo '%s' | pfctl -a %s -f -", natRule, anchorName)
	if err := exec.Command("sh", "-c", cmd).Run(); err != nil {
		return fmt.Errorf("add pf nat rule: %w", err)
	}

	if !strings.Contains(string(out), anchorName) {
		exec.Command("pfctl", "-e").Run()
	}

	log.Printf("NAT enabled: %s traffic via %s SNAT'd", t.tunNet, localInterface)
	return nil
}

func (t *Interface) removeNATMacOS() error {
	exec.Command("pfctl", "-a", "net-tunnel", "-F", "all").Run()
	return nil
}

func (t *Interface) addNATLinux(localInterface string) error {
	exec.Command("iptables", "-t", "nat", "-C", "POSTROUTING", "-s", t.tunNet, "-o", localInterface, "-j", "MASQUERADE").Run()

	cmds := [][]string{
		{"iptables", "-t", "nat", "-A", "POSTROUTING", "-s", t.tunNet, "-o", localInterface, "-j", "MASQUERADE"},
		{"iptables", "-A", "FORWARD", "-i", t.name, "-o", localInterface, "-j", "ACCEPT"},
		{"iptables", "-A", "FORWARD", "-i", localInterface, "-o", t.name, "-j", "ACCEPT"},
	}

	var lastErr error
	for _, cmd := range cmds {
		if err := exec.Command(cmd[0], cmd[1:]...).Run(); err != nil {
			lastErr = err
		}
	}

	if lastErr == nil {
		log.Printf("NAT enabled: %s via %s MASQUERADEd", t.tunNet, localInterface)
	}
	return lastErr
}

func (t *Interface) removeNATLinux(localInterface string) error {
	cmds := [][]string{
		{"iptables", "-t", "nat", "-D", "POSTROUTING", "-s", t.tunNet, "-o", localInterface, "-j", "MASQUERADE"},
		{"iptables", "-D", "FORWARD", "-i", t.name, "-o", localInterface, "-j", "ACCEPT"},
		{"iptables", "-D", "FORWARD", "-i", localInterface, "-o", t.name, "-j", "ACCEPT"},
	}

	var lastErr error
	for _, cmd := range cmds {
		if err := exec.Command(cmd[0], cmd[1:]...).Run(); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

func (t *Interface) addNATWindows(localInterface string) error {
	natName := "NetTunnelNAT"
	ps := "powershell"
	rmCmd := fmt.Sprintf(`Remove-NetNat -Name '%s' -Confirm:$false -ErrorAction:SilentlyContinue`, natName)
	exec.Command(ps, "-Command", rmCmd).Run()

	createCmd := fmt.Sprintf(`New-NetNat -Name '%s' -InternalIPInterfaceAddressPrefix '%s' -ErrorAction:Stop`, natName, t.tunNet)
	if out, err := exec.Command(ps, "-Command", createCmd).CombinedOutput(); err != nil {
		return fmt.Errorf("create NetNat failed: %w\noutput: %s", err, string(out))
	}

	verifyCmd := fmt.Sprintf(`if (-not (Get-NetNat -Name '%s' -ErrorAction:SilentlyContinue)) { throw 'NAT not found' }`, natName)
	if out, err := exec.Command(ps, "-Command", verifyCmd).CombinedOutput(); err != nil {
		return fmt.Errorf("NAT verification failed: %w\noutput: %s", err, string(out))
	}

	log.Printf("NAT enabled: %s via %s", t.tunNet, localInterface)
	return nil
}

func (t *Interface) removeNATWindows(localInterface string) error {
	natName := "NetTunnelNAT"
	cmd := fmt.Sprintf(`Remove-NetNat -Name '%s' -Confirm:$false -ErrorAction:SilentlyContinue`, natName)
	return exec.Command("powershell", "-Command", cmd).Run()
}

func AutoDetectSubnet() (string, string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", "", fmt.Errorf("list interfaces: %w", err)
	}

	var subnets []string
	firstIface := ""

	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		if iface.Flags&net.FlagPointToPoint != 0 {
			continue
		}
		if iface.Name == "utun" || iface.Name == "tun" || iface.Name == "tap" {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			if ipNet.IP.IsLoopback() {
				continue
			}
			if ipNet.IP.To4() == nil {
				continue
			}
			ones, _ := ipNet.Mask.Size()
			if ones == 32 {
				continue
			}
			subnet := fmt.Sprintf("%s/%d", ipNet.IP.Mask(ipNet.Mask).String(), ones)
			subnets = append(subnets, subnet)
			if firstIface == "" {
				firstIface = iface.Name
			}
		}
	}

	if len(subnets) == 0 {
		return "", "", fmt.Errorf("no suitable LAN interface found")
	}

	return strings.Join(subnets, ","), firstIface, nil
}

func FindInterfaceForSubnet(subnet string) (string, error) {
	_, targetNet, err := net.ParseCIDR(subnet)
	if err != nil {
		return "", fmt.Errorf("parse subnet: %w", err)
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return "", fmt.Errorf("list interfaces: %w", err)
	}

	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if iface.Flags&net.FlagUp == 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			if ipNet.IP.IsLoopback() {
				continue
			}
			if targetNet.Contains(ipNet.IP) {
				return iface.Name, nil
			}
		}
	}

	return "", fmt.Errorf("no interface found for subnet %s", subnet)
}

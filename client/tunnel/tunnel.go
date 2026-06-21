package tunnel

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"os/exec"
	"runtime"
	"strings"

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
		{"route", "-n", "add", "-net", "10.0.0.0/24", "-interface", t.name},
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
		{"ip", "route", "add", "10.0.0.0/24", "dev", t.name},
	}
	for _, cmd := range cmds {
		if err := exec.Command(cmd[0], cmd[1:]...).Run(); err != nil {
			return fmt.Errorf("run %v: %w", cmd, err)
		}
	}
	return nil
}

func (t *Interface) configureWindows(ip, cidr string, mtu int) error {
	cmds := []string{
		fmt.Sprintf(`netsh interface ip set address "%s" static %s %s`,
			t.name, ip, "255.255.255.0"),
		fmt.Sprintf(`netsh interface ip set subinterface "%s" mtu=%d store=persistent`,
			t.name, mtu),
		fmt.Sprintf(`route add 10.0.0.0 mask 255.255.255.0 %s metric 1`, ip),
	}
	for _, cmd := range cmds {
		if err := exec.Command("cmd", "/c", cmd).Run(); err != nil {
			return fmt.Errorf("run %s: %w", cmd, err)
		}
	}
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
		cmd := fmt.Sprintf(`route add %s mask %s %s metric 1`, ipnet.IP.String(), mask, t.tunIP)
		return exec.Command("cmd", "/c", cmd).Run()
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
		cmd := fmt.Sprintf(`route delete %s`, ipnet.IP.String())
		return exec.Command("cmd", "/c", cmd).Run()
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

func (t *Interface) Name() string {
	return t.name
}

func (t *Interface) ReadPacket() ([]byte, error) {
	buf := make([]byte, t.mtu+200)
	n, err := t.Read(buf)
	if err != nil {
		return nil, err
	}
	data := buf[:n]

	if t.isTAP {
		return t.extractIPFromEthernet(data)
	}
	return data, nil
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

func (t *Interface) wrapEthernet(ipPacket []byte) []byte {
	frame := make([]byte, ethernetHeaderLen+len(ipPacket))

	if t.macAddr == nil {
		iface, err := net.InterfaceByName(t.name)
		if err == nil {
			t.macAddr = iface.HardwareAddr
		}
	}

	if t.macAddr != nil && len(t.macAddr) == 6 {
		copy(frame[0:6], t.macAddr)
		copy(frame[6:12], t.macAddr)
	} else {
		frame[5] = 0x01
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
		cmds := []string{
			`reg add HKLM\SYSTEM\CurrentControlSet\Services\Tcpip\Parameters /v IPEnableRouter /t REG_DWORD /d 1 /f`,
			`netsh routing ip nat install`,
		}
		for _, cmd := range cmds {
			if err := exec.Command("cmd", "/c", cmd).Run(); err != nil {
				log.Printf("Warning: %s: %v", cmd, err)
			}
		}
		return nil
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
	cmds := []string{
		fmt.Sprintf(`powershell -Command "New-NetNat -Name 'NetTunnelNAT' -InternalIPInterfaceAddressPrefix '%s' -ExternalIPInterfaceAddressPrefix '0.0.0.0/0'"`, t.tunNet),
		fmt.Sprintf(`netsh routing ip nat add interface "%s" mode=private`, t.name),
		fmt.Sprintf(`netsh routing ip nat add interface "%s" mode=public`, localInterface),
		fmt.Sprintf(`netsh interface ipv4 set route prefix=%s interface="%s" nexthop=%s metric=1`, t.tunNet, t.name, t.tunIP),
	}

	var lastErr error
	for _, cmd := range cmds {
		if err := exec.Command("cmd", "/c", cmd).Run(); err != nil {
			lastErr = err
		}
	}
	if lastErr == nil {
		log.Printf("NAT enabled: %s via %s", t.tunNet, localInterface)
	}
	return lastErr
}

func (t *Interface) removeNATWindows(localInterface string) error {
	cmds := []string{
		`powershell -Command "Remove-NetNat -Name 'NetTunnelNAT' -Confirm:$false -ErrorAction:SilentlyContinue"`,
		fmt.Sprintf(`netsh routing ip nat delete interface "%s"`, t.name),
		fmt.Sprintf(`netsh routing ip nat delete interface "%s"`, localInterface),
	}

	var lastErr error
	for _, cmd := range cmds {
		if err := exec.Command("cmd", "/c", cmd).Run(); err != nil {
			lastErr = err
		}
	}
	return lastErr
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

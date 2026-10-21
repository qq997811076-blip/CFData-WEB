package main

import (
	"bufio"
	"io"
	"math/rand"
	"net"
	"path/filepath"
	"regexp"
	"strings"
)


func readNonEmptyLines(reader io.Reader) ([]string, error) {
	scanner := bufio.NewScanner(reader)
	var lines []string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	return lines, scanner.Err()
}

func parseIPList(content string) ([]string, error) {
	lines, err := readNonEmptyLines(strings.NewReader(content))
	if err != nil {
		return nil, err
	}
	for i, line := range lines {
		if strings.Contains(line, "/") {
			continue
		}
		ip := net.ParseIP(line)
		if ip == nil {
			continue
		}
		if ip.To4() != nil {
			lines[i] = line + "/32"
		} else {
			lines[i] = line + "/128"
		}
	}
	return lines, nil
}

func parseTraceResponse(body string) map[string]string {
	result := make(map[string]string)
	lines := strings.Split(body, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			result[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return result
}

func getIPType(ip string) string {
	if ip == "" {
		return "未知"
	}
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return "无效IP"
	}
	if parsedIP.To4() != nil {
		return "IPv4"
	}
	return "IPv6"
}

func safeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "ip.csv"
	}
	base := filepath.Base(name)
	if strings.TrimSpace(base) == "" {
		return "ip.csv"
	}
	return base
}

const maxIPv4SubnetExpansion = 1 << 16

func expandCIDRTo24s(subnet string) []string {
	_, ipNet, err := net.ParseCIDR(strings.TrimSpace(subnet))
	if err != nil {
		return nil
	}

	ones, _ := ipNet.Mask.Size()
	if ones >= 24 {
		return []string{subnet}
	}

	baseIP := ipNet.IP.To4()
	if baseIP == nil {
		return nil
	}

	shift := 24 - ones
	if shift > 16 {
		shift = 16
	}
	count := 1 << uint(shift)
	if count > maxIPv4SubnetExpansion {
		count = maxIPv4SubnetExpansion
	}

	networkInt := uint32(baseIP[0])<<24 | uint32(baseIP[1])<<16 | uint32(baseIP[2])<<8 | uint32(baseIP[3])
	subnets := make([]string, 0, count)
	for i := 0; i < count; i++ {
		cur := networkInt + uint32(i)*256
		ip := net.IPv4(byte(cur>>24), byte(cur>>16), byte(cur>>8), 0)
		subnets = append(subnets, ip.String()+"/24")
	}

	return subnets
}

func randomIPFromCIDR(subnet string) (string, bool) {
	ip, ipNet, err := net.ParseCIDR(strings.TrimSpace(subnet))
	if err != nil {
		return "", false
	}

	baseIP := ip.Mask(ipNet.Mask)
	if ipv4 := baseIP.To4(); ipv4 != nil {
		baseIP = ipv4
	} else {
		baseIP = baseIP.To16()
		if baseIP == nil {
			return "", false
		}
	}

	randomIP := make(net.IP, len(baseIP))
	copy(randomIP, baseIP)

	ones, bits := ipNet.Mask.Size()
	hostBits := bits - ones
	if hostBits <= 0 {
		return randomIP.String(), true
	}

	for i := len(randomIP) - 1; i >= 0 && hostBits > 0; i-- {
		bitsThisByte := hostBits
		if bitsThisByte > 8 {
			bitsThisByte = 8
		}
		maxValue := 1 << bitsThisByte
		randomIP[i] |= byte(rand.Intn(maxValue))
		hostBits -= bitsThisByte
	}

	return randomIP.String(), true
}

func getRandomIPv4s(ipList []string) []string {
	var randomIPs []string
	for _, subnet := range ipList {
		subnets := expandCIDRTo24s(subnet)
		if subnets == nil {
			continue
		}
		for _, cidr := range subnets {
			randomIP, ok := randomIPFromCIDR(cidr)
			if !ok {
				continue
			}
			if net.ParseIP(randomIP).To4() == nil {
				continue
			}
			randomIPs = append(randomIPs, randomIP)
		}
	}
	return randomIPs
}

const maxIPv6SubnetExpansion = 1 << 16

func expandCIDRTo48s(subnet string) []string {
	_, ipNet, err := net.ParseCIDR(strings.TrimSpace(subnet))
	if err != nil {
		return nil
	}

	ones, _ := ipNet.Mask.Size()
	if ones >= 48 {
		return []string{subnet}
	}

	baseIP := ipNet.IP.To16()
	if baseIP == nil {
		return nil
	}

	shift := 48 - ones
	if shift > 16 {
		shift = 16
	}
	count := 1 << uint(shift)
	if count > maxIPv6SubnetExpansion {
		count = maxIPv6SubnetExpansion
	}

	var networkInt uint64
	for j := 0; j < 6; j++ {
		networkInt = (networkInt << 8) | uint64(baseIP[j])
	}

	subnets := make([]string, 0, count)
	for i := 0; i < count; i++ {
		cur := networkInt + uint64(i)
		ip := make(net.IP, 16)
		for j := 5; j >= 0; j-- {
			ip[j] = byte(cur & 0xFF)
			cur >>= 8
		}
		subnets = append(subnets, ip.String()+"/48")
	}

	return subnets
}

func getRandomIPv6s(ipList []string) []string {
	var randomIPs []string
	for _, subnet := range ipList {
		subnets := expandCIDRTo48s(subnet)
		if subnets == nil {
			continue
		}
		for _, cidr := range subnets {
			randomIP, ok := randomIPFromCIDR(cidr)
			if !ok {
				continue
			}
			parsed := net.ParseIP(randomIP)
			if parsed == nil || parsed.To4() != nil {
				continue
			}
			randomIPs = append(randomIPs, randomIP)
		}
	}
	return randomIPs
}


// extractImportedIPv4s follows the reference import behavior: recognize IPv4/CIDR tokens,
// expand bounded CIDR ranges, and preserve first-seen order while deduplicating.
func extractImportedIPv4s(content string) []string {
	tokenRE := regexp.MustCompile(`([0-9]{1,3}(?:\.[0-9]{1,3}){3}(?:\s*/\s*[0-9]{1,2})?)`)
	seen := make(map[string]struct{})
	var ips []string
	for _, match := range tokenRE.FindAllStringSubmatchIndex(content, -1) {
		start, end := match[0], match[1]
		if start > 0 {
			prev := content[start-1]
			if (prev >= '0' && prev <= '9') || prev == '.' { continue }
		}
		if end < len(content) {
			next := content[end]
			if (next >= '0' && next <= '9') || next == '.' || next == '/' { continue }
		}
		token := strings.ReplaceAll(strings.TrimSpace(content[match[2]:match[3]]), " ", "")
		if strings.Contains(token, "/") {
			_, network, err := net.ParseCIDR(token)
			if err != nil || network.IP.To4() == nil { continue }
			ones, bits := network.Mask.Size()
			if bits != 32 || ones < 0 || ones > 32 { continue }
			count := uint64(1) << uint(32-ones)
			if count > 1<<20 { continue }
			base4 := network.IP.To4()
			start := uint64(base4[0])<<24 | uint64(base4[1])<<16 | uint64(base4[2])<<8 | uint64(base4[3])
			for offset := uint64(0); offset < count; offset++ {
				v := uint32(start + offset)
				ip := net.IPv4(byte(v>>24), byte(v>>16), byte(v>>8), byte(v)).String()
				if _, ok := seen[ip]; !ok { seen[ip] = struct{}{}; ips = append(ips, ip) }
			}
			continue
		}
		ip := net.ParseIP(token)
		if ip == nil || ip.To4() == nil { continue }
		canonical := ip.To4().String()
		if _, ok := seen[canonical]; !ok { seen[canonical] = struct{}{}; ips = append(ips, canonical) }
	}
	return ips
}

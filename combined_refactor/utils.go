package main

import (
	"bufio"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	urlCandidateRegex         = regexp.MustCompile(`(?i)\b(?:https?|ws|wss)://[^\s|，,]+`)
	bracketIPv6PortRegex      = regexp.MustCompile(`\[([0-9A-Fa-f:.]+)\]\s*[:#，,]\s*(\d{1,5})`)
	hostPortSeparatorRegex    = regexp.MustCompile(`(?i)(^|[^A-Za-z0-9.:-])((?:\d{1,3}\.){3}\d{1,3}|[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+)\s*[:：#，,]\s*(\d{1,5})`)
	hostPortWhitespaceRegex   = regexp.MustCompile(`(?i)(^|[^A-Za-z0-9.:-])((?:\d{1,3}\.){3}\d{1,3}|[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+)\s+(\d{1,5})(?:\s|$)`)
	bareIPv4OrDomainRegex     = regexp.MustCompile(`(?i)(^|[^A-Za-z0-9.:-])((?:\d{1,3}\.){3}\d{1,3}|[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+)(?:\s|$|[|，,。；;、])`)
	possibleBareIPv6LineRegex = regexp.MustCompile(`(?i)([0-9a-f]{1,4}:[0-9a-f:.]+)`)
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

func looksLikeIPv4Literal(host string) bool {
	parts := strings.Split(host, ".")
	if len(parts) != 4 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

func isValidDomain(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if len(host) < 4 || len(host) > 253 || !strings.Contains(host, ".") {
		return false
	}
	if strings.Trim(host, "0123456789.") == "" {
		return false
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return false
			}
		}
	}
	return true
}

func parsePort(value string) (int, bool) {
	value = strings.TrimSpace(value)
	port, err := strconv.Atoi(value)
	if err != nil || !isValidPort(port) {
		return 0, false
	}
	return port, true
}

func isValidPort(port int) bool {
	return port > 0 && port <= 65535
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

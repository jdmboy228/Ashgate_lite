package lite

import (
	"net"
	"strings"
)

const hidden = "•••"

// maskHost hides the middle of a proxy address the way the design shows it:
// «185.12.•••.7», «nl-ams.•••.net».
func maskHost(host string) string {
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			parts := strings.Split(v4.String(), ".")
			parts[2] = hidden
			return strings.Join(parts, ".")
		}
		groups := strings.Split(ip.String(), ":")
		if len(groups) > 2 {
			return groups[0] + ":" + hidden + ":" + groups[len(groups)-1]
		}
		return host
	}
	labels := strings.Split(host, ".")
	switch {
	case len(labels) >= 3:
		return labels[0] + "." + hidden + "." + labels[len(labels)-1]
	case len(labels) == 2:
		return hidden + "." + labels[1]
	default:
		return host
	}
}

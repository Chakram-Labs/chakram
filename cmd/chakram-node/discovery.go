package main

import (
	"net"
	"strings"
)

const (
	defaultSeedsDNS = "seeds.chakram.one"
	maxLinkDepth    = 3
)

// seedList holds peer addresses discovered via DNS.
type seedList struct {
	CometPeers  []string // nodeID@host:26656
	GethEnodes  []string // enode://pubkey@host:30303
	APIURLs     []string // https://host  (for genesis + node-info)
	SnapTrusted string   // "height:0xhash" from snap= field (first seen wins)
}

// resolveSeedsDNS queries one or more DNS seed names and returns all
// discovered peers. TXT record format (one field per record):
//
//	comet=nodeID@host:26656
//	geth=enode://pubkey@host:30303
//	api=https://host
//	snap=HEIGHT:0xHASH  (trusted snapshot: height and EVM block hash)
//	link=other.seeds.example.com   (followed recursively, max 3 hops)
//
// link= lets any organisation publish their own seed list under their own
// domain and have it included automatically — no code change needed.
func resolveSeedsDNS(names []string) seedList {
	var result seedList
	visited := make(map[string]bool)

	var walk func(name string, depth int)
	walk = func(name string, depth int) {
		name = strings.TrimSpace(name)
		if name == "" || depth > maxLinkDepth || visited[name] {
			return
		}
		visited[name] = true

		txts, err := net.LookupTXT(name)
		if err != nil {
			return
		}
		for _, record := range txts {
			// A single TXT record may contain space-separated fields
			// (some DNS providers join multi-string records with spaces).
			for _, field := range strings.Fields(record) {
				k, v, ok := strings.Cut(field, "=")
				if !ok || v == "" {
					continue
				}
				switch k {
				case "comet":
					result.CometPeers = appendUniq(result.CometPeers, v)
				case "geth":
					result.GethEnodes = appendUniq(result.GethEnodes, v)
				case "api":
					result.APIURLs = appendUniq(result.APIURLs, v)
				case "snap":
					if result.SnapTrusted == "" {
						result.SnapTrusted = v // "height:0xhash"
					}
				case "link":
					walk(v, depth+1)
				}
			}
		}
	}

	for _, name := range names {
		walk(name, 0)
	}
	return result
}

// appendUniq appends v to slice only if not already present.
func appendUniq(slice []string, v string) []string {
	for _, existing := range slice {
		if existing == v {
			return slice
		}
	}
	return append(slice, v)
}

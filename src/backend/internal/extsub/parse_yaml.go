package extsub

import (
	"gopkg.in/yaml.v3"
)

// parseYAML 解析 Clash/mihomo YAML 的 proxies 段。
func parseYAML(body []byte) ([]Node, bool) {
	var doc map[string]any
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return nil, false
	}
	rawProxies, ok := doc["proxies"].([]any)
	if !ok || len(rawProxies) == 0 {
		return nil, false
	}
	var nodes []Node
	for _, raw := range rawProxies {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name := toString(entry["name"])
		nodeType := toString(entry["type"])
		server := toString(entry["server"])
		port := toInt(entry["port"])
		if name == "" || nodeType == "" || server == "" || port == 0 {
			continue
		}
		extra := make(map[string]any)
		for key, value := range entry {
			switch key {
			case "name", "type", "server", "port":
			default:
				extra[key] = value
			}
		}
		nodes = append(nodes, Node{
			Name: name, Type: nodeType, Server: server, Port: port, Extra: extra,
		})
	}
	if len(nodes) == 0 {
		return nil, false
	}
	return nodes, true
}

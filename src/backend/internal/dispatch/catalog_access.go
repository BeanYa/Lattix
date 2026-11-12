package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"lattix/shared"
)

// provisionCatalogNodes installs credentials on published non-shared chain
// exits that may still have the previous per-user allocation deployed.
func (d *Dispatcher) provisionCatalogNodes(ctx context.Context, serverID int64) error {
	ids, err := d.st.PublishedCatalogNodeIDsForServer(ctx, serverID)
	if err != nil {
		return err
	}
	byUser := make(map[string]map[string]shared.UserNodeParams)
	for _, id := range ids {
		node, err := d.st.NodeByID(ctx, id)
		if err != nil {
			return err
		}
		var config shared.VirtualConfig
		if err := json.Unmarshal(node.ConfigTemplate, &config); err != nil {
			return err
		}
		if !shared.HasUserList(config.Protocol) {
			continue
		}
		users, err := d.st.CatalogNodeUserUUIDs(ctx, id, time.Now())
		if err != nil {
			return err
		}
		tag := shared.NodeTag(id)
		params := shared.UserNodeParams{Protocol: config.Protocol, Flow: config.Flow, Method: config.Method}
		for _, uuid := range users {
			if byUser[uuid] == nil {
				byUser[uuid] = make(map[string]shared.UserNodeParams)
			}
			byUser[uuid][tag] = params
		}
	}
	var result error
	for uuid, nodes := range byUser {
		_, err := d.Enqueue(ctx, serverID, shared.TypeAddUser, shared.AddUserPayload{UUID: uuid, Nodes: nodes})
		result = errors.Join(result, err)
	}
	return result
}

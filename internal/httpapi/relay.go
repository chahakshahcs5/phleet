package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/hashicorp/yamux"

	"phleet/internal/agentproto"
	"phleet/internal/engines"
	"phleet/internal/model"
	"phleet/internal/tunnel"
)

// ensureAddonRelayRow creates or finds the facade row without contacting the
// agent. Standby provisioning starts the listener itself from the spec, which
// removes any ordering race with pg_basebackup.
func (s *Server) ensureAddonRelayRow(a model.Addon, serverID string) (int, error) {
	if serverID == "" {
		return 0, errors.New("no target phone")
	}
	if serverID == a.PinnedServer {
		return a.Port, nil
	}
	if a.Kind == engines.KindFile {
		return 0, errors.New("a SQLite file cannot be reached from another phone")
	}
	if r, err := s.st.GetAddonRelay(a.ID, serverID); err == nil {
		return r.Port, nil
	}
	port, err := s.allocateRelayPort(serverID)
	if err != nil {
		return 0, err
	}
	if err := s.st.UpsertAddonRelay(model.AddonRelay{
		AddonID: a.ID, ServerID: serverID, Port: port, UpdatedAt: time.Now().Unix(),
	}); err != nil {
		return 0, err
	}
	_ = s.st.AddEvent("scheduler", "addon.relay", a.ID, fmt.Sprintf("%s:%d", serverID, port))
	return port, nil
}

// ensureAddonRelay returns the loopback port an app phone should use to reach
// an addon hosted elsewhere, creating and instructing the facade if needed.
func (s *Server) ensureAddonRelay(a model.Addon, serverID string) (int, error) {
	port, err := s.ensureAddonRelayRow(a, serverID)
	if err != nil {
		return 0, err
	}
	if serverID == a.PinnedServer {
		return port, nil
	}
	if err := s.sendRelay(a, serverID, port); err != nil {
		return port, err
	}
	return port, nil
}

func (s *Server) sendRelay(a model.Addon, serverID string, port int) error {
	if !s.hub.Online(serverID) {
		return errors.New("the phone running this service is offline")
	}
	// A synchronous call guarantees the listener is bound before a standby
	// provisioning plan starts pg_basebackup through it.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := s.hub.Call(ctx, serverID, agentproto.TypeAddonRelay, agentproto.AddonRelaySpec{
		AddonID: a.ID, Action: "start", LocalPort: port,
		TargetServer: a.PinnedServer, TargetPort: a.Port,
	})
	return err
}

func (s *Server) syncRelaysForServer(serverID string) {
	relays, err := s.st.ListAddonRelaysForServer(serverID)
	if err != nil {
		return
	}
	for _, r := range relays {
		a, err := s.st.GetAddon(r.AddonID)
		if err != nil || a.PinnedServer == serverID {
			_ = s.st.DeleteAddonRelay(r.AddonID, serverID)
			continue
		}
		if err := s.sendRelay(a, serverID, r.Port); err != nil {
			s.log.Warn("addon relay sync", "addon", a.Slug, "server", serverID, "err", err)
		}
	}
}

func (s *Server) stopAddonRelays(a model.Addon) {
	relays, err := s.st.ListAddonRelays(a.ID)
	if err != nil {
		return
	}
	for _, r := range relays {
		if s.hub.Online(r.ServerID) {
			_ = s.hub.Send(r.ServerID, agentproto.TypeAddonRelay, agentproto.AddonRelaySpec{
				AddonID: a.ID, Action: "stop", LocalPort: r.Port,
			})
		}
		_ = s.st.DeleteAddonRelay(a.ID, r.ServerID)
	}
}

// serveRelayStreams accepts streams that an agent opens for its loopback
// facade. It runs alongside the data tunnel's own HTTP stream handler.
func (s *Server) serveRelayStreams(serverID string, sess *yamux.Session) {
	for {
		stream, err := sess.AcceptStream()
		if err != nil {
			return
		}
		go s.handleRelayStream(serverID, stream)
	}
}

func (s *Server) handleRelayStream(serverID string, stream *yamux.Stream) {
	defer stream.Close()
	_ = stream.SetDeadline(time.Now().Add(30 * time.Second))

	var req tunnel.RequestHeader
	if err := tunnel.ReadFrame(stream, &req); err != nil {
		return
	}
	relayErr := func(msg string) {
		_ = tunnel.WriteFrame(stream, tunnel.ResponseHeader{Status: http.StatusBadGateway, Error: msg})
	}
	if req.Kind != "relay" || req.AddonID == "" {
		relayErr("invalid relay frame")
		return
	}
	// Only a phone that was explicitly given a facade may use it.
	if _, err := s.st.GetAddonRelay(req.AddonID, serverID); err != nil {
		_ = tunnel.WriteFrame(stream, tunnel.ResponseHeader{Status: http.StatusForbidden, Error: "relay not allowed for this addon"})
		return
	}
	a, err := s.st.GetAddon(req.AddonID)
	if err != nil {
		relayErr("addon not found")
		return
	}
	placements, err := s.st.ListAddonPlacements(a.ID)
	if err != nil {
		relayErr("addon has no placement")
		return
	}
	target := model.AddonPlacement{}
	for _, pl := range placements {
		if (pl.Role == "primary" || pl.Role == "") && pl.Desired != "stopped" {
			target = pl
			break
		}
	}
	if target.ServerID == "" {
		relayErr("no primary for this addon")
		return
	}
	up, ok := s.hub.Tunnel(target.ServerID)
	if !ok {
		relayErr("the database phone is not connected")
		return
	}
	sess, err := tunnel.ProxyTCP(up, a.Port, "", "tcp")
	if err != nil {
		relayErr(err.Error())
		return
	}
	_ = stream.SetDeadline(time.Time{})
	if err := tunnel.WriteFrame(stream, tunnel.ResponseHeader{Status: http.StatusOK, ContentLength: -1}); err != nil {
		_ = sess.Close()
		return
	}
	tunnel.Bridge(stream, sess)
}

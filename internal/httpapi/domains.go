package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"phleet/internal/cloudflare"
)

func (s *Server) handleCreateCustomDomain(w http.ResponseWriter, r *http.Request) {
	p, err := s.st.GetProject(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	host := strings.ToLower(strings.TrimSpace(p.CustomHostname))
	if host == "" {
		apiErr(w, 400, errors.New("set a custom domain on this project first"))
		return
	}
	conf, err := cloudflare.LoadConfig(s.cfg.CloudflaredConfig())
	if err != nil {
		apiErr(w, 400, errors.New("run phleet setup-cloudflare first"))
		return
	}
	if conf.APIToken == "" || conf.ZoneID == "" || conf.TunnelID == "" {
		apiErr(w, 400, errors.New("the saved Cloudflare setup has no API token, zone or tunnel"))
		return
	}
	domain := strings.ToLower(strings.TrimSpace(conf.Domain))
	if domain == "" || !(host == domain || strings.HasSuffix(host, "."+domain)) {
		apiErr(w, 400, fmt.Errorf("%s is outside %s; add the CNAME manually in that zone", host, domain))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	result, err := cloudflare.New(conf.APIToken).UpsertCNAME(ctx, conf.ZoneID, host, conf.TunnelID+".cfargotunnel.com")
	if err != nil {
		apiErr(w, 502, err)
		return
	}
	_ = s.st.AddEvent("ui", "project.domain", p.ID, host+" "+result)
	writeJSON(w, 200, map[string]any{"ok": true, "result": result, "hostname": host})
}

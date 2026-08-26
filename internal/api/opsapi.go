package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/campaign"
	"github.com/cploutarchou/arb-chain-bot/internal/marketdata"
)

// RecorderController is the in-process recording control surface.
type RecorderController interface {
	StartSession() (string, error)
	Stop() (string, error)
	Status() marketdata.RecorderStatus
}

// CampaignService launches and lists §80 campaign runs.
type CampaignService interface {
	Start(req campaign.Request, actor string) (campaign.Run, error)
	List(ctx context.Context, limit int) ([]campaign.Run, error)
	Get(ctx context.Context, id string) (campaign.Run, error)
}

// opsRoutes: recording control and campaign runs — the console's
// replacement for `make record` / `make campaign` (docs/deployment.md).
// Reads need view:system; mutations need their own operator permission
// plus CSRF, and every mutation is audited.
func (s *Server) opsRoutes(mux *http.ServeMux) {
	recorderGate := func(action string, fn func(RecorderController) (string, error)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if s.Recorder == nil {
				WriteError(w, http.StatusNotFound, "recorder_absent", "recorder not available in this profile", correlationID(r))
				return
			}
			p, _ := PrincipalFrom(r.Context())
			id, err := fn(s.Recorder)
			switch {
			case errors.Is(err, marketdata.ErrRecorderRunning), errors.Is(err, marketdata.ErrRecorderIdle):
				WriteError(w, http.StatusConflict, "recorder_state", err.Error(), correlationID(r))
				return
			case err != nil:
				s.log.Error("recorder control failed", "action", action, "error", err)
				WriteError(w, http.StatusInternalServerError, "recorder_failed", "recorder control failed", correlationID(r))
				return
			}
			s.audit(r, p.UserID, action, "recording:"+id)
			s.log.Info("recorder control", "actor", p.UserID, "action", action, "session", id)
			WriteData(w, http.StatusOK, map[string]any{"session_id": id, "recorder": s.Recorder.Status()})
		}
	}
	mux.HandleFunc("POST /api/v1/recordings/start", s.requirePerm(auth.PermRecordControl, s.requireCSRF(
		recorderGate("recording.start", func(c RecorderController) (string, error) { return c.StartSession() }))))
	mux.HandleFunc("POST /api/v1/recordings/stop", s.requirePerm(auth.PermRecordControl, s.requireCSRF(
		recorderGate("recording.stop", func(c RecorderController) (string, error) { return c.Stop() }))))

	needCampaigns := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if s.Campaigns == nil {
				WriteError(w, http.StatusNotFound, "campaigns_absent", "campaign runner not available in this profile", correlationID(r))
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("GET /api/v1/campaigns", s.requirePerm(auth.PermViewSystem, needCampaigns(func(w http.ResponseWriter, r *http.Request) {
		runs, err := s.Campaigns.List(r.Context(), limitParam(r))
		s.writeListResult(w, r, "runs", runs, err)
	})))
	mux.HandleFunc("GET /api/v1/campaigns/{id}", s.requirePerm(auth.PermViewSystem, needCampaigns(func(w http.ResponseWriter, r *http.Request) {
		run, err := s.Campaigns.Get(r.Context(), r.PathValue("id"))
		switch {
		case errors.Is(err, campaign.ErrNotFound):
			WriteError(w, http.StatusNotFound, "not_found", "campaign run not found", correlationID(r))
		case err != nil:
			s.writeListResult(w, r, "run", nil, err)
		default:
			WriteData(w, http.StatusOK, map[string]any{"run": run})
		}
	})))
	mux.HandleFunc("POST /api/v1/campaigns", s.requirePerm(auth.PermCampaignRun, s.requireCSRF(needCampaigns(func(w http.ResponseWriter, r *http.Request) {
		var req campaign.Request
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid_body", "malformed campaign request", correlationID(r))
			return
		}
		p, _ := PrincipalFrom(r.Context())
		run, err := s.Campaigns.Start(req, p.UserID)
		switch {
		case errors.Is(err, campaign.ErrBusy):
			WriteError(w, http.StatusConflict, "campaign_busy", err.Error(), correlationID(r))
			return
		case err != nil:
			WriteError(w, http.StatusBadRequest, "invalid_request", err.Error(), correlationID(r))
			return
		}
		s.audit(r, p.UserID, "campaign.start", "campaign:"+run.ID)
		s.log.Info("campaign started", "actor", p.UserID, "run", run.ID, "recording", run.Recording)
		WriteData(w, http.StatusAccepted, map[string]any{"run": run})
	}))))
}

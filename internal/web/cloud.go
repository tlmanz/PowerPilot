package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/tlmanz/PowerPilot/internal/cloud"
)

// Cloud is the Cloud setting: Off, Remote or Publish (package cloud).
type Cloud interface {
	Status() cloud.Status
	Apply(ctx context.Context, r cloud.Request) (cloud.Status, error)
}

// Battery is the UPS's battery health on a powerpilot-server, which works it
// out from the UPS's readings (cloud.Remote). path is /battery or
// /battery/replaced; bodies and replies pass through unchanged.
type Battery interface {
	Battery(ctx context.Context, method, path string, body []byte) (status int, reply []byte, err error)
}

var (
	errNoCloud   = errors.New("the Cloud setting isn't available here")
	errNoBattery = errors.New("battery health comes from powerpilot-server: switch Cloud to Remote mode")
)

// battery relays GET and PUT /api/battery and POST /api/battery/replaced to
// the server.
func (s *server) battery(w http.ResponseWriter, r *http.Request) {
	if s.Battery == nil {
		writeError(w, http.StatusConflict, errNoBattery)
		return
	}
	var body json.RawMessage
	if r.Method != http.MethodGet && !decode(w, r, &body) {
		return
	}
	status, reply, err := s.Battery.Battery(r.Context(), r.Method, strings.TrimPrefix(r.URL.Path, "/api"), body)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(reply)
}

func (s *server) getCloud(w http.ResponseWriter, r *http.Request) {
	if s.Cloud == nil {
		writeError(w, http.StatusNotFound, errNoCloud)
		return
	}
	writeJSON(w, http.StatusOK, s.Cloud.Status())
}

// putCloud checks the new setting with the server and saves it, then restarts
// the service in the new mode, as after an update.
func (s *server) putCloud(w http.ResponseWriter, r *http.Request) {
	if s.Cloud == nil {
		writeError(w, http.StatusNotFound, errNoCloud)
		return
	}
	var req cloud.Request
	if !decode(w, r, &req) {
		return
	}
	st, err := s.Cloud.Apply(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.mon.Note("info", "Cloud set to "+st.Mode+", restarting")
	writeJSON(w, http.StatusOK, st)
	if s.Updater == nil {
		return
	}
	go func() {
		time.Sleep(500 * time.Millisecond) // let the reply reach the dashboard
		if err := s.Updater.Restart(); err != nil {
			s.mon.Note("alert", "Could not restart for the Cloud setting: "+err.Error())
		}
	}()
}

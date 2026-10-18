package web

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/tlmanz/PowerPilot/internal/cloud"
)

// Cloud is the Cloud setting: Off, Remote or Publish (package cloud).
type Cloud interface {
	Status() cloud.Status
	Apply(ctx context.Context, r cloud.Request) (cloud.Status, error)
}

var errNoCloud = errors.New("the Cloud setting isn't available here")

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

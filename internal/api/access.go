package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/anand34577/gorget/internal/core"
	"github.com/anand34577/gorget/internal/store"
)

type accessRequestBody struct {
	Target  string `json:"target"`
	Ports   string `json:"ports"`
	Reason  string `json:"reason"`
	Minutes int    `json:"minutes"`
}

// listAccessRequests: administrators see everything, other people their own requests.
func (a *API) listAccessRequests(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	userID := p.user.ID
	if p.can(permManageNet) || p.can(permRead) {
		userID = ""
	}
	list, err := a.core.Store.ListAccessRequests(r.Context(), userID, 500)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if list == nil {
		list = []store.AccessRequest{}
	}
	writeJSON(w, http.StatusOK, list)
}

// createAccessRequest: anyone who can write may ask for temporary access.
func (a *API) createAccessRequest(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	if !p.canWrite() {
		forbidden(w)
		return
	}
	var req accessRequestBody
	if !decode(w, r, &req) {
		return
	}
	res, err := a.core.RequestAccess(r.Context(), p.user, core.AccessInput{Target: req.Target, Ports: req.Ports, Reason: req.Reason, Minutes: req.Minutes})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

type accessDecisionBody struct {
	Minutes int `json:"minutes"`
}

func (a *API) decideAccess(decision string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := who(r)
		if !p.can(permManageNet) {
			forbidden(w)
			return
		}
		var body accessDecisionBody
		if r.ContentLength > 0 && !decode(w, r, &body) {
			return
		}
		res, err := a.core.DecideAccess(r.Context(), a.actor(r), chi.URLParam(r, "id"), decision, body.Minutes)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

type accessGrantBody struct {
	UserID string `json:"user_id"`
	accessRequestBody
}

// grantAccess: an administrator gives someone (for example a guest) temporary access directly.
func (a *API) grantAccess(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	if !p.can(permManageNet) {
		forbidden(w)
		return
	}
	var req accessGrantBody
	if !decode(w, r, &req) {
		return
	}
	u, err := a.core.Store.GetUser(r.Context(), req.UserID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	res, err := a.core.GrantAccess(r.Context(), a.actor(r), u, core.AccessInput{Target: req.Target, Ports: req.Ports, Reason: req.Reason, Minutes: req.Minutes})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

package api

import (
	"errors"
	"net/http"
	"net/netip"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/anand34577/gorget/internal/core"
	"github.com/anand34577/gorget/internal/policy"
	"github.com/anand34577/gorget/internal/store"
)

func (a *API) getPolicy(w http.ResponseWriter, r *http.Request) {
	if !who(r).can(permRead) {
		forbidden(w)
		return
	}
	pv, err := a.core.Store.CurrentPolicy(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, pv)
}

type policyAnalysis struct {
	Valid    bool                `json:"valid"`
	Problems []string            `json:"problems"`
	Tests    []policy.TestResult `json:"tests"`
	Rules    int                 `json:"rules"`
	Parsed   *policy.Policy      `json:"parsed,omitempty"`
}

// analyse parses, compiles against the live network and runs the embedded tests.
func (a *API) analyse(doc string) (policyAnalysis, *policy.Policy) {
	res := policyAnalysis{Problems: []string{}, Tests: []policy.TestResult{}}
	p, err := policy.Parse(doc)
	if err != nil {
		var ve *policy.ValidationError
		if errors.As(err, &ve) {
			res.Problems = ve.Problems
		} else {
			res.Problems = []string{err.Error()}
		}
		return res, nil
	}
	s := a.core.Coord.Snapshot()
	c := p.Compile(s.Env)
	res.Tests = c.RunTests(s.Env)
	if res.Tests == nil {
		res.Tests = []policy.TestResult{}
	}
	res.Rules = len(p.ACLs)
	res.Valid = true
	for _, t := range res.Tests {
		if !t.Passed {
			res.Valid = false
		}
	}
	res.Parsed = p
	return res, p
}

func (a *API) validatePolicy(w http.ResponseWriter, r *http.Request) {
	if !who(r).can(permRead) {
		forbidden(w)
		return
	}
	var req struct {
		Document string `json:"document"`
	}
	if !decode(w, r, &req) {
		return
	}
	res, _ := a.analyse(req.Document)
	writeJSON(w, http.StatusOK, res)
}

func (a *API) putPolicy(w http.ResponseWriter, r *http.Request) {
	if !who(r).can(permManageNet) {
		forbidden(w)
		return
	}
	var req struct {
		Document      string `json:"document"`
		Comment       string `json:"comment"`
		ExpectVersion int64  `json:"expect_version"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len(req.Document) > 512*1024 {
		badRequest(w, "policy document is too large")
		return
	}
	res, _ := a.analyse(req.Document)
	if !res.Valid {
		writeErrDetails(w, http.StatusUnprocessableEntity, "policy_invalid", "the policy has errors or failing tests", res)
		return
	}
	pv, err := a.core.Store.SavePolicy(r.Context(), req.Document, req.Comment, who(r).user.Email, req.ExpectVersion)
	if errors.Is(err, store.ErrConflict) {
		writeErr(w, http.StatusConflict, "conflict", "the policy was changed by someone else; reload and try again")
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.core.Audit(r.Context(), a.actor(r), "policy.update", "policy", strconv.FormatInt(pv.Version, 10), "version "+strconv.FormatInt(pv.Version, 10), map[string]any{"comment": req.Comment, "rules": res.Rules})
	a.core.Bus.Publish(core.EvPolicyUpdated, map[string]any{"version": pv.Version})
	a.core.Coord.Trigger()
	writeJSON(w, http.StatusOK, pv)
}

func (a *API) policyVersions(w http.ResponseWriter, r *http.Request) {
	if !who(r).can(permRead) {
		forbidden(w)
		return
	}
	vs, err := a.core.Store.ListPolicyVersions(r.Context(), queryInt(r, "limit", 50))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if vs == nil {
		vs = []store.PolicyVersion{}
	}
	writeJSON(w, http.StatusOK, vs)
}

func (a *API) policyVersion(w http.ResponseWriter, r *http.Request) {
	if !who(r).can(permRead) {
		forbidden(w)
		return
	}
	v, _ := strconv.ParseInt(chi.URLParam(r, "v"), 10, 64)
	pv, err := a.core.Store.GetPolicyVersion(r.Context(), v)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, pv)
}

func (a *API) restorePolicy(w http.ResponseWriter, r *http.Request) {
	if !who(r).can(permManageNet) {
		forbidden(w)
		return
	}
	v, _ := strconv.ParseInt(chi.URLParam(r, "v"), 10, 64)
	old, err := a.core.Store.GetPolicyVersion(r.Context(), v)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if res, _ := a.analyse(old.Document); !res.Valid {
		writeErrDetails(w, http.StatusUnprocessableEntity, "policy_invalid", "that version no longer validates against the current network", res)
		return
	}
	pv, err := a.core.Store.SavePolicy(r.Context(), old.Document, "restored from version "+strconv.FormatInt(v, 10), who(r).user.Email, 0)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.core.Audit(r.Context(), a.actor(r), "policy.restore", "policy", strconv.FormatInt(pv.Version, 10), "version "+strconv.FormatInt(v, 10), nil)
	a.core.Bus.Publish(core.EvPolicyUpdated, map[string]any{"version": pv.Version})
	a.core.Coord.Trigger()
	writeJSON(w, http.StatusOK, pv)
}

// checkPolicy is the access simulator: may src reach dst:port?
func (a *API) checkPolicy(w http.ResponseWriter, r *http.Request) {
	if !who(r).can(permRead) {
		forbidden(w)
		return
	}
	var req struct {
		Src      string `json:"src"` // device id, device name or IP
		Dst      string `json:"dst"`
		Port     int    `json:"port"`
		Proto    string `json:"proto"`
		Document string `json:"document"` // optional: simulate an unsaved policy
	}
	if !decode(w, r, &req) {
		return
	}
	s := a.core.Coord.Snapshot()
	compiled := s.Compiled
	if req.Document != "" {
		res, p := a.analyse(req.Document)
		if p == nil {
			writeErrDetails(w, http.StatusUnprocessableEntity, "policy_invalid", "the policy has errors", res)
			return
		}
		compiled = p.Compile(s.Env)
	}
	src, ok := resolveAddr(s, req.Src)
	if !ok {
		badRequest(w, "unknown source (use a device name, id or IP address)")
		return
	}
	dst, ok := resolveAddr(s, req.Dst)
	if !ok {
		badRequest(w, "unknown destination (use a device name, id or IP address)")
		return
	}
	if req.Port < 0 || req.Port > 65535 {
		badRequest(w, "invalid port")
		return
	}
	dec := compiled.Check(src, dst, uint16(req.Port), req.Proto)
	writeJSON(w, http.StatusOK, map[string]any{"decision": dec, "src_ip": src.String(), "dst_ip": dst.String()})
}

func resolveAddr(s *core.Snapshot, v string) (netip.Addr, bool) {
	if a, err := netip.ParseAddr(v); err == nil {
		return a, true
	}
	for _, d := range s.Devices {
		if d.ID == v || d.Name == v {
			a, err := netip.ParseAddr(d.IPv4)
			return a, err == nil
		}
	}
	return netip.Addr{}, false
}

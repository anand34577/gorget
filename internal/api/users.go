package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/anand34577/gorget/internal/auth"
	"github.com/anand34577/gorget/internal/core"
	"github.com/anand34577/gorget/internal/secrets"
	"github.com/anand34577/gorget/internal/store"
)

type userView struct {
	*store.User
	Groups      []string `json:"groups"`
	DeviceCount int      `json:"device_count"`
	Passkeys    int      `json:"passkeys"`
}

func (a *API) listUsers(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	ctx := r.Context()
	users, err := a.core.Store.ListUsers(ctx)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	s := a.core.Coord.Snapshot()
	counts := map[string]int{}
	for _, d := range s.Devices {
		counts[d.Owner()]++
	}
	out := []userView{}
	for i := range users {
		u := &users[i]
		if !p.can(permRead) && u.ID != p.user.ID {
			continue
		}
		v := userView{User: u, Groups: []string{}, DeviceCount: counts[u.ID]}
		for name, members := range s.Groups {
			for _, m := range members {
				if m == u.ID {
					v.Groups = append(v.Groups, name)
				}
			}
		}
		if pk, err := a.core.Store.ListWebAuthnCredentials(ctx, u.ID); err == nil {
			v.Passkeys = len(pk)
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
}

type createUserReq struct {
	Email    string   `json:"email"`
	Name     string   `json:"name"`
	Role     string   `json:"role"`
	Password string   `json:"password"`
	Groups   []string `json:"groups"`
	// Invite emails a link to choose a password (needs email to be set up).
	Invite bool `json:"invite"`
}

func (a *API) createUser(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	if !p.can(permManageUsers) {
		forbidden(w)
		return
	}
	var req createUserReq
	if !decode(w, r, &req) {
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if !strings.Contains(req.Email, "@") || len(req.Email) > 254 {
		badRequest(w, "a valid email is required")
		return
	}
	if req.Role == "" {
		req.Role = store.RoleUser
	}
	if !store.ValidRole(req.Role) {
		badRequest(w, "invalid role")
		return
	}
	if req.Role == store.RoleOwner && !p.can(permOwner) {
		writeErr(w, http.StatusForbidden, "forbidden", "only owners can create owners")
		return
	}
	temp := ""
	if req.Password == "" {
		temp = secrets.RandomToken("", 12)
		req.Password = temp
	} else if err := auth.ValidatePassword(req.Password); err != nil {
		badRequest(w, err.Error())
		return
	}
	u := &store.User{
		ID: secrets.RandomID(), Email: req.Email, Name: strings.TrimSpace(req.Name), Role: req.Role,
		PasswordHash: secrets.HashPassword(req.Password), Provider: "local", RecoveryCodes: store.StringList{},
		MustChangePassword: true, CreatedAt: store.Now(),
	}
	ctx := r.Context()
	if err := a.core.Store.CreateUser(ctx, u); err != nil {
		a.fail(w, r, err)
		return
	}
	for _, gid := range req.Groups {
		_ = a.core.Store.AddGroupMember(ctx, gid, u.ID)
	}
	a.core.Audit(ctx, a.actor(r), "user.create", "user", u.ID, u.Email, map[string]any{"role": u.Role})
	a.core.Bus.Publish(core.EvUserCreated, map[string]string{"id": u.ID, "email": u.Email})
	a.core.Coord.Trigger()
	if req.Invite && temp != "" {
		if err := a.core.SendInvitation(ctx, u, p.user.Email); err == nil {
			// The person chooses their own password; nobody else needs to see one.
			writeJSON(w, http.StatusCreated, map[string]any{"user": u, "temporary_password": "", "invited": true})
			return
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{"user": u, "temporary_password": temp, "invited": false})
}

type updateUserReq struct {
	Name     *string `json:"name"`
	Role     *string `json:"role"`
	Disabled *bool   `json:"disabled"`
}

func (a *API) loadUserForAdmin(w http.ResponseWriter, r *http.Request) (*store.User, bool) {
	p := who(r)
	if !p.can(permManageUsers) {
		forbidden(w)
		return nil, false
	}
	u, err := a.core.Store.GetUser(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err)
		return nil, false
	}
	if u.Role == store.RoleOwner && !p.can(permOwner) {
		writeErr(w, http.StatusForbidden, "forbidden", "only owners can manage owners")
		return nil, false
	}
	return u, true
}

func (a *API) updateUser(w http.ResponseWriter, r *http.Request) {
	u, ok := a.loadUserForAdmin(w, r)
	if !ok {
		return
	}
	p := who(r)
	var req updateUserReq
	if !decode(w, r, &req) {
		return
	}
	ctx := r.Context()
	changes := map[string]any{}
	if req.Name != nil {
		u.Name = strings.TrimSpace(*req.Name)
		changes["name"] = u.Name
	}
	if req.Role != nil && *req.Role != u.Role {
		if !store.ValidRole(*req.Role) {
			badRequest(w, "invalid role")
			return
		}
		if *req.Role == store.RoleOwner && !p.can(permOwner) {
			forbidden(w)
			return
		}
		if u.Role == store.RoleOwner {
			if n, _ := a.core.Store.CountOwners(ctx); n <= 1 {
				badRequest(w, "the last owner cannot be demoted")
				return
			}
		}
		changes["role"] = map[string]string{"from": u.Role, "to": *req.Role}
		u.Role = *req.Role
	}
	if req.Disabled != nil && *req.Disabled != u.Disabled {
		if u.ID == p.user.ID {
			badRequest(w, "you cannot disable your own account")
			return
		}
		if *req.Disabled && u.Role == store.RoleOwner {
			if n, _ := a.core.Store.CountOwners(ctx); n <= 1 {
				badRequest(w, "the last owner cannot be disabled")
				return
			}
		}
		u.Disabled = *req.Disabled
		changes["disabled"] = u.Disabled
	}
	if req.Disabled != nil && *req.Disabled {
		u.LockedUntil = 0
	}
	if err := a.core.Store.UpdateUser(ctx, u); err != nil {
		a.fail(w, r, err)
		return
	}
	if u.Disabled {
		_ = a.core.Store.DeleteUserSessions(ctx, u.ID)
	}
	a.core.Audit(ctx, a.actor(r), "user.update", "user", u.ID, u.Email, changes)
	a.core.Bus.Publish(core.EvUserUpdated, map[string]string{"id": u.ID})
	a.core.Coord.Trigger()
	writeJSON(w, http.StatusOK, u)
}

func (a *API) deleteUser(w http.ResponseWriter, r *http.Request) {
	u, ok := a.loadUserForAdmin(w, r)
	if !ok {
		return
	}
	p := who(r)
	if u.ID == p.user.ID {
		badRequest(w, "you cannot delete your own account")
		return
	}
	if !requireRecentAuth(w, p) {
		return
	}
	ctx := r.Context()
	if u.Role == store.RoleOwner {
		if n, _ := a.core.Store.CountOwners(ctx); n <= 1 {
			badRequest(w, "the last owner cannot be deleted")
			return
		}
	}
	// Devices owned by the user are removed by cascade; notify their sessions first.
	for _, d := range a.core.Coord.Snapshot().Devices {
		if d.Owner() == u.ID {
			a.core.Coord.Notify(d.ID, core.Notice{Kind: "logged_out", Message: "Your account was removed."})
		}
	}
	if err := a.core.Store.DeleteUser(ctx, u.ID); err != nil {
		a.fail(w, r, err)
		return
	}
	a.core.Audit(ctx, a.actor(r), "user.delete", "user", u.ID, u.Email, nil)
	a.core.Bus.Publish(core.EvUserDeleted, map[string]string{"id": u.ID})
	a.core.Coord.Trigger()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *API) resetUserMFA(w http.ResponseWriter, r *http.Request) {
	u, ok := a.loadUserForAdmin(w, r)
	if !ok {
		return
	}
	if !requireRecentAuth(w, who(r)) {
		return
	}
	ctx := r.Context()
	if err := a.auth.DisableTOTP(ctx, u); err != nil {
		a.fail(w, r, err)
		return
	}
	creds, _ := a.core.Store.ListWebAuthnCredentials(ctx, u.ID)
	for _, c := range creds {
		_ = a.core.Store.DeleteWebAuthnCredential(ctx, u.ID, c.ID)
	}
	_ = a.core.Store.DeleteUserSessions(ctx, u.ID)
	a.core.Audit(ctx, a.actor(r), "user.mfa_reset", "user", u.ID, u.Email, nil)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *API) resetUserPassword(w http.ResponseWriter, r *http.Request) {
	u, ok := a.loadUserForAdmin(w, r)
	if !ok {
		return
	}
	if !requireRecentAuth(w, who(r)) {
		return
	}
	temp := secrets.RandomToken("", 12)
	u.PasswordHash = secrets.HashPassword(temp)
	u.MustChangePassword = true
	u.LockedUntil, u.FailedLogins = 0, 0
	ctx := r.Context()
	if err := a.core.Store.UpdateUser(ctx, u); err != nil {
		a.fail(w, r, err)
		return
	}
	_ = a.core.Store.DeleteUserSessions(ctx, u.ID)
	a.core.Audit(ctx, a.actor(r), "user.password_reset", "user", u.ID, u.Email, nil)
	writeJSON(w, http.StatusOK, map[string]string{"temporary_password": temp})
}

func (a *API) revokeUserSessions(w http.ResponseWriter, r *http.Request) {
	u, ok := a.loadUserForAdmin(w, r)
	if !ok {
		return
	}
	if err := a.core.Store.DeleteUserSessions(r.Context(), u.ID); err != nil {
		a.fail(w, r, err)
		return
	}
	a.core.Audit(r.Context(), a.actor(r), "user.sessions_revoke", "user", u.ID, u.Email, nil)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---------- groups ----------

func (a *API) listGroups(w http.ResponseWriter, r *http.Request) {
	gs, err := a.core.Store.ListGroups(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if gs == nil {
		gs = []store.Group{}
	}
	if !who(r).can(permRead) {
		// Regular users only see group names they belong to.
		var mine []store.Group
		for _, g := range gs {
			for _, m := range g.Members {
				if m == who(r).user.ID {
					mine = append(mine, store.Group{ID: g.ID, Name: g.Name, Description: g.Description, Members: []string{}})
				}
			}
		}
		gs = mine
		if gs == nil {
			gs = []store.Group{}
		}
	}
	writeJSON(w, http.StatusOK, gs)
}

type groupReq struct {
	Name        *string   `json:"name"`
	Description *string   `json:"description"`
	Members     *[]string `json:"members"`
}

func canManageGroups(p *principal) bool { return p.can(permManageUsers) || p.can(permManageNet) }

func (a *API) createGroup(w http.ResponseWriter, r *http.Request) {
	if !canManageGroups(who(r)) {
		forbidden(w)
		return
	}
	var req groupReq
	if !decode(w, r, &req) {
		return
	}
	if req.Name == nil || core.SanitizeName(*req.Name) != *req.Name {
		badRequest(w, "group name must be lowercase letters, digits and dashes")
		return
	}
	g := &store.Group{ID: secrets.RandomID(), Name: *req.Name, Source: "local", CreatedAt: store.Now(), Members: []string{}}
	if req.Description != nil {
		g.Description = *req.Description
	}
	if req.Members != nil {
		g.Members = *req.Members
	}
	if err := a.core.Store.CreateGroup(r.Context(), g); err != nil {
		a.fail(w, r, err)
		return
	}
	a.core.Audit(r.Context(), a.actor(r), "group.create", "group", g.ID, g.Name, map[string]any{"members": len(g.Members)})
	a.core.Coord.Trigger()
	writeJSON(w, http.StatusCreated, g)
}

func (a *API) updateGroup(w http.ResponseWriter, r *http.Request) {
	if !canManageGroups(who(r)) {
		forbidden(w)
		return
	}
	var req groupReq
	if !decode(w, r, &req) {
		return
	}
	g, err := a.core.Store.GetGroup(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if req.Name != nil {
		if core.SanitizeName(*req.Name) != *req.Name {
			badRequest(w, "group name must be lowercase letters, digits and dashes")
			return
		}
		g.Name = *req.Name
	}
	if req.Description != nil {
		g.Description = *req.Description
	}
	if req.Members != nil {
		g.Members = *req.Members
	}
	if err := a.core.Store.UpdateGroup(r.Context(), g); err != nil {
		a.fail(w, r, err)
		return
	}
	a.core.Audit(r.Context(), a.actor(r), "group.update", "group", g.ID, g.Name, map[string]any{"members": len(g.Members)})
	a.core.Coord.Trigger()
	writeJSON(w, http.StatusOK, g)
}

func (a *API) deleteGroup(w http.ResponseWriter, r *http.Request) {
	if !canManageGroups(who(r)) {
		forbidden(w)
		return
	}
	g, err := a.core.Store.GetGroup(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.core.Store.DeleteGroup(r.Context(), g.ID); err != nil {
		a.fail(w, r, err)
		return
	}
	a.core.Audit(r.Context(), a.actor(r), "group.delete", "group", g.ID, g.Name, nil)
	a.core.Coord.Trigger()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

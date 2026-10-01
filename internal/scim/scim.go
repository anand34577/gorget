// Package scim implements the SCIM 2.0 provisioning endpoint (RFC 7643/7644) that
// identity providers such as Okta, Entra ID and Keycloak use to create, update and
// deprovision users and groups automatically.
//
// Users provisioned here have no password; they sign in through the single sign-on
// provider, which links to the account by email address.
package scim

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/anand34577/gorget/internal/core"
	"github.com/anand34577/gorget/internal/secrets"
	"github.com/anand34577/gorget/internal/store"
)

const (
	schemaUser   = "urn:ietf:params:scim:schemas:core:2.0:User"
	schemaGroup  = "urn:ietf:params:scim:schemas:core:2.0:Group"
	schemaList   = "urn:ietf:params:scim:api:messages:2.0:ListResponse"
	schemaError  = "urn:ietf:params:scim:api:messages:2.0:Error"
	schemaPatch  = "urn:ietf:params:scim:api:messages:2.0:PatchOp"
	sourceGroups = "scim"
)

// Handler serves /scim/v2.
type Handler struct {
	c       *core.Core
	baseURL string
}

// New creates the handler; baseURL is the public URL of the server.
func New(c *core.Core, baseURL string) *Handler {
	return &Handler{c: c, baseURL: strings.TrimRight(baseURL, "/")}
}

// Routes returns the router to mount at /scim/v2.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(h.auth)
	r.Get("/ServiceProviderConfig", h.serviceProviderConfig)
	r.Get("/ResourceTypes", h.resourceTypes)
	r.Get("/Schemas", h.schemas)
	r.Get("/Users", h.listUsers)
	r.Post("/Users", h.createUser)
	r.Get("/Users/{id}", h.getUser)
	r.Put("/Users/{id}", h.replaceUser)
	r.Patch("/Users/{id}", h.patchUser)
	r.Delete("/Users/{id}", h.deleteUser)
	r.Get("/Groups", h.listGroups)
	r.Post("/Groups", h.createGroup)
	r.Get("/Groups/{id}", h.getGroup)
	r.Put("/Groups/{id}", h.replaceGroup)
	r.Patch("/Groups/{id}", h.patchGroup)
	r.Delete("/Groups/{id}", h.deleteGroup)
	return r
}

// ---------- plumbing ----------

func (h *Handler) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if tok == "" || tok == r.Header.Get("Authorization") || !h.c.CheckSCIMToken(r.Context(), tok) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="gorget-scim"`)
			writeError(w, http.StatusUnauthorized, "", "invalid or missing SCIM token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/scim+json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, scimType, detail string) {
	e := map[string]any{"schemas": []string{schemaError}, "status": strconv.Itoa(code), "detail": detail}
	if scimType != "" {
		e["scimType"] = scimType
	}
	writeJSON(w, code, e)
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalidSyntax", "invalid JSON: "+err.Error())
		return false
	}
	return true
}

func (h *Handler) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "", "resource not found")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "uniqueness", "a resource with this identifier already exists")
	default:
		h.c.Log.Error("SCIM request failed", "err", err)
		writeError(w, http.StatusInternalServerError, "", "internal error")
	}
}

func ts(unix int64) string { return time.Unix(unix, 0).UTC().Format(time.RFC3339) }

func page(r *http.Request) (start, count int) {
	start, count = 1, 100
	if v, err := strconv.Atoi(r.URL.Query().Get("startIndex")); err == nil && v > 0 {
		start = v
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("count")); err == nil && v >= 0 && v < 1000 {
		count = v
	}
	return
}

func listResponse(w http.ResponseWriter, resources []any, total, start int) {
	if resources == nil {
		resources = []any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"schemas": []string{schemaList}, "totalResults": total, "startIndex": start, "itemsPerPage": len(resources), "Resources": resources})
}

// parseEq understands the one filter IdPs actually send: attr eq "value".
func parseEq(filter string) (attr, value string, ok bool) {
	f := strings.TrimSpace(filter)
	if f == "" {
		return "", "", true
	}
	parts := strings.SplitN(f, " ", 3)
	if len(parts) != 3 || !strings.EqualFold(parts[1], "eq") {
		return "", "", false
	}
	return strings.ToLower(parts[0]), strings.Trim(strings.TrimSpace(parts[2]), `"`), true
}

// ---------- discovery ----------

func (h *Handler) serviceProviderConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"schemas":        []string{"urn:ietf:params:scim:schemas:core:2.0:ServiceProviderConfig"},
		"patch":          map[string]bool{"supported": true},
		"bulk":           map[string]any{"supported": false, "maxOperations": 0, "maxPayloadSize": 0},
		"filter":         map[string]any{"supported": true, "maxResults": 200},
		"changePassword": map[string]bool{"supported": false},
		"sort":           map[string]bool{"supported": false},
		"etag":           map[string]bool{"supported": false},
		"authenticationSchemes": []map[string]string{{
			"type": "oauthbearertoken", "name": "Bearer token", "description": "Authenticate with the SCIM token from Settings > Single sign-on.",
		}},
	})
}

func (h *Handler) resourceTypes(w http.ResponseWriter, r *http.Request) {
	rt := func(id, name, endpoint, schema string) map[string]any {
		return map[string]any{"schemas": []string{"urn:ietf:params:scim:schemas:core:2.0:ResourceType"}, "id": id, "name": name, "endpoint": endpoint, "schema": schema}
	}
	writeJSON(w, http.StatusOK, map[string]any{"schemas": []string{schemaList}, "totalResults": 2, "Resources": []any{
		rt("User", "User", "/Users", schemaUser), rt("Group", "Group", "/Groups", schemaGroup),
	}})
}

func (h *Handler) schemas(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"schemas": []string{schemaList}, "totalResults": 2, "Resources": []any{
		map[string]any{"id": schemaUser, "name": "User", "description": "User account"},
		map[string]any{"id": schemaGroup, "name": "Group", "description": "Group of users"},
	}})
}

// ---------- users ----------

type scimName struct {
	Formatted  string `json:"formatted,omitempty"`
	GivenName  string `json:"givenName,omitempty"`
	FamilyName string `json:"familyName,omitempty"`
}

type scimEmail struct {
	Value   string `json:"value"`
	Primary bool   `json:"primary,omitempty"`
	Type    string `json:"type,omitempty"`
}

type scimUser struct {
	Schemas     []string    `json:"schemas,omitempty"`
	ID          string      `json:"id,omitempty"`
	ExternalID  string      `json:"externalId,omitempty"`
	UserName    string      `json:"userName"`
	Name        *scimName   `json:"name,omitempty"`
	DisplayName string      `json:"displayName,omitempty"`
	Emails      []scimEmail `json:"emails,omitempty"`
	Active      *bool       `json:"active,omitempty"`
	Groups      []any       `json:"groups,omitempty"`
	Meta        any         `json:"meta,omitempty"`
}

func (h *Handler) userResource(u *store.User, groups map[string][]string) scimUser {
	active := !u.Disabled
	out := scimUser{
		Schemas: []string{schemaUser}, ID: u.ID, UserName: u.Email, DisplayName: u.Name,
		Name: &scimName{Formatted: u.Name}, Emails: []scimEmail{{Value: u.Email, Primary: true, Type: "work"}}, Active: &active,
		Meta: map[string]string{"resourceType": "User", "created": ts(u.CreatedAt), "lastModified": ts(max(u.CreatedAt, u.LastLoginAt)), "location": h.baseURL + "/scim/v2/Users/" + u.ID},
	}
	for _, g := range groups[u.ID] {
		out.Groups = append(out.Groups, map[string]string{"display": g})
	}
	return out
}

func (h *Handler) userGroups(ctx context.Context) map[string][]string {
	out := map[string][]string{}
	groups, err := h.c.Store.ListGroups(ctx)
	if err != nil {
		return out
	}
	for _, g := range groups {
		for _, m := range g.Members {
			out[m] = append(out[m], g.Name)
		}
	}
	return out
}

func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	attr, val, ok := parseEq(r.URL.Query().Get("filter"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalidFilter", `only filters of the form attribute eq "value" are supported`)
		return
	}
	users, err := h.c.Store.ListUsers(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	groups := h.userGroups(r.Context())
	var matched []any
	for i := range users {
		u := &users[i]
		switch attr {
		case "":
		case "username", "emails.value":
			if !strings.EqualFold(u.Email, val) {
				continue
			}
		case "externalid":
			continue // we don't store external ids
		case "id":
			if u.ID != val {
				continue
			}
		default:
			writeError(w, http.StatusBadRequest, "invalidFilter", "unsupported filter attribute "+attr)
			return
		}
		matched = append(matched, h.userResource(u, groups))
	}
	start, count := page(r)
	total := len(matched)
	lo := min(start-1, total)
	hi := min(lo+count, total)
	listResponse(w, matched[lo:hi], total, start)
}

func (h *Handler) getUser(w http.ResponseWriter, r *http.Request) {
	u, err := h.c.Store.GetUser(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h.userResource(u, h.userGroups(r.Context())))
}

func emailOf(in scimUser) string {
	email := strings.ToLower(strings.TrimSpace(in.UserName))
	if !strings.Contains(email, "@") {
		for _, e := range in.Emails {
			if e.Primary || email == "" || !strings.Contains(email, "@") {
				email = strings.ToLower(strings.TrimSpace(e.Value))
			}
		}
	}
	return email
}

func displayOf(in scimUser) string {
	switch {
	case in.DisplayName != "":
		return in.DisplayName
	case in.Name != nil && in.Name.Formatted != "":
		return in.Name.Formatted
	case in.Name != nil:
		return strings.TrimSpace(in.Name.GivenName + " " + in.Name.FamilyName)
	}
	return ""
}

func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) {
	var in scimUser
	if !decode(w, r, &in) {
		return
	}
	email := emailOf(in)
	if !strings.Contains(email, "@") || len(email) > 254 {
		writeError(w, http.StatusBadRequest, "invalidValue", "userName must be an email address")
		return
	}
	ctx := r.Context()
	if _, err := h.c.Store.GetUserByEmail(ctx, email); err == nil {
		writeError(w, http.StatusConflict, "uniqueness", "a user with this userName already exists")
		return
	}
	u := &store.User{
		ID: secrets.RandomID(), Email: email, Name: displayOf(in), Role: store.RoleUser, Provider: "local",
		RecoveryCodes: store.StringList{}, CreatedAt: store.Now(), Disabled: in.Active != nil && !*in.Active,
	}
	if err := h.c.Store.CreateUser(ctx, u); err != nil {
		h.fail(w, err)
		return
	}
	h.c.Audit(ctx, core.Actor{ID: "scim", Name: "scim"}, "user.create", "user", u.ID, u.Email, map[string]string{"via": "scim"})
	h.c.Bus.Publish(core.EvUserCreated, map[string]string{"id": u.ID, "email": u.Email})
	h.c.Coord.Trigger()
	w.Header().Set("Location", h.baseURL+"/scim/v2/Users/"+u.ID)
	writeJSON(w, http.StatusCreated, h.userResource(u, nil))
}

// applyUser copies changes onto an existing user; the role is never changed by SCIM.
func (h *Handler) applyUser(u *store.User, in scimUser) error {
	if in.UserName != "" || len(in.Emails) > 0 {
		email := emailOf(in)
		if !strings.Contains(email, "@") {
			return errors.New("userName must be an email address")
		}
		u.Email = email
	}
	if d := displayOf(in); d != "" {
		u.Name = d
	}
	if in.Active != nil {
		u.Disabled = !*in.Active
	}
	return nil
}

func (h *Handler) saveUser(w http.ResponseWriter, r *http.Request, u *store.User, wasDisabled bool) {
	ctx := r.Context()
	if u.Role == store.RoleOwner && u.Disabled && !wasDisabled {
		// Same rule as deletion: an identity provider must never be able to lock the
		// owners out of their own network.
		writeError(w, http.StatusForbidden, "", "owners can't be deactivated through SCIM; change their role in the console first")
		return
	}
	if err := h.c.Store.UpdateUser(ctx, u); err != nil {
		h.fail(w, err)
		return
	}
	if u.Disabled && !wasDisabled {
		// Deprovisioned: end their sessions and sign their devices out.
		_ = h.c.Store.DeleteUserSessions(ctx, u.ID)
		for _, d := range h.c.Coord.Snapshot().Devices {
			if d.Owner() == u.ID {
				h.c.Coord.Notify(d.ID, core.Notice{Kind: "logged_out", Message: "Your account was deactivated."})
			}
		}
	}
	h.c.Audit(ctx, core.Actor{ID: "scim", Name: "scim"}, "user.update", "user", u.ID, u.Email, map[string]any{"via": "scim", "disabled": u.Disabled})
	h.c.Bus.Publish(core.EvUserUpdated, map[string]string{"id": u.ID})
	h.c.Coord.Trigger()
	writeJSON(w, http.StatusOK, h.userResource(u, h.userGroups(r.Context())))
}

func (h *Handler) replaceUser(w http.ResponseWriter, r *http.Request) {
	u, err := h.c.Store.GetUser(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	var in scimUser
	if !decode(w, r, &in) {
		return
	}
	was := u.Disabled
	if err := h.applyUser(u, in); err != nil {
		writeError(w, http.StatusBadRequest, "invalidValue", err.Error())
		return
	}
	h.saveUser(w, r, u, was)
}

type patchOp struct {
	Op    string          `json:"op"`
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value"`
}

type patchBody struct {
	Schemas    []string  `json:"schemas"`
	Operations []patchOp `json:"Operations"`
}

func (h *Handler) patchUser(w http.ResponseWriter, r *http.Request) {
	u, err := h.c.Store.GetUser(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	var body patchBody
	if !decode(w, r, &body) {
		return
	}
	was := u.Disabled
	for _, op := range body.Operations {
		switch strings.ToLower(op.Op) {
		case "replace", "add":
		case "remove":
			continue // nothing in our user model can be removed
		default:
			writeError(w, http.StatusBadRequest, "invalidSyntax", "unsupported operation "+op.Op)
			return
		}
		in := scimUser{}
		path := strings.ToLower(op.Path)
		switch path {
		case "":
			// Okta/Entra send a whole partial resource (or {"active": false}) without a path.
			if json.Unmarshal(op.Value, &in) != nil {
				var flat map[string]any
				if json.Unmarshal(op.Value, &flat) == nil {
					if v, ok := flat["active"].(bool); ok {
						in.Active = &v
					}
				}
			}
		case "active":
			in.Active = boolValue(op.Value)
		case "displayname":
			in.DisplayName = stringValue(op.Value)
		case "username":
			in.UserName = stringValue(op.Value)
		case "name.formatted", "name.givenname", "name.familyname":
			in.Name = &scimName{Formatted: stringValue(op.Value)}
			if path != "name.formatted" {
				in.Name = &scimName{GivenName: stringValue(op.Value)}
			}
		case "emails", `emails[type eq "work"].value`, "emails.value":
			in.UserName = emailFromValue(op.Value)
		default:
			continue // unknown attributes are ignored, as most IdPs expect
		}
		if err := h.applyUser(u, in); err != nil {
			writeError(w, http.StatusBadRequest, "invalidValue", err.Error())
			return
		}
	}
	h.saveUser(w, r, u, was)
}

func boolValue(raw json.RawMessage) *bool {
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		return &b
	}
	var s string
	if json.Unmarshal(raw, &s) == nil { // Entra sends "False"/"True" as strings
		b = strings.EqualFold(s, "true")
		return &b
	}
	return nil
}

func stringValue(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

func emailFromValue(raw json.RawMessage) string {
	if s := stringValue(raw); s != "" {
		return s
	}
	var list []scimEmail
	if json.Unmarshal(raw, &list) == nil && len(list) > 0 {
		return list[0].Value
	}
	return ""
}

func (h *Handler) deleteUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u, err := h.c.Store.GetUser(ctx, chi.URLParam(r, "id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	if u.Role == store.RoleOwner {
		writeError(w, http.StatusForbidden, "", "owners can't be deleted through SCIM; deactivate or remove them in the console")
		return
	}
	for _, d := range h.c.Coord.Snapshot().Devices {
		if d.Owner() == u.ID {
			h.c.Coord.Notify(d.ID, core.Notice{Kind: "logged_out", Message: "Your account was removed."})
		}
	}
	if err := h.c.Store.DeleteUser(ctx, u.ID); err != nil {
		h.fail(w, err)
		return
	}
	h.c.Audit(ctx, core.Actor{ID: "scim", Name: "scim"}, "user.delete", "user", u.ID, u.Email, map[string]string{"via": "scim"})
	h.c.Bus.Publish(core.EvUserDeleted, map[string]string{"id": u.ID})
	h.c.Coord.Trigger()
	w.WriteHeader(http.StatusNoContent)
}

// ---------- groups ----------

type scimMember struct {
	Value   string `json:"value"`
	Display string `json:"display,omitempty"`
}

type scimGroup struct {
	Schemas     []string     `json:"schemas,omitempty"`
	ID          string       `json:"id,omitempty"`
	DisplayName string       `json:"displayName"`
	Members     []scimMember `json:"members,omitempty"`
	Meta        any          `json:"meta,omitempty"`
}

func (h *Handler) groupResource(g *store.Group) scimGroup {
	out := scimGroup{Schemas: []string{schemaGroup}, ID: g.ID, DisplayName: g.Name,
		Meta: map[string]string{"resourceType": "Group", "created": ts(g.CreatedAt), "location": h.baseURL + "/scim/v2/Groups/" + g.ID}}
	for _, m := range g.Members {
		out.Members = append(out.Members, scimMember{Value: m})
	}
	return out
}

func (h *Handler) listGroups(w http.ResponseWriter, r *http.Request) {
	attr, val, ok := parseEq(r.URL.Query().Get("filter"))
	if !ok || (attr != "" && attr != "displayname") {
		writeError(w, http.StatusBadRequest, "invalidFilter", `only displayName eq "value" is supported`)
		return
	}
	groups, err := h.c.Store.ListGroups(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	var matched []any
	for i := range groups {
		if attr == "" || strings.EqualFold(groups[i].Name, val) {
			matched = append(matched, h.groupResource(&groups[i]))
		}
	}
	start, count := page(r)
	total := len(matched)
	lo := min(start-1, total)
	hi := min(lo+count, total)
	listResponse(w, matched[lo:hi], total, start)
}

func (h *Handler) getGroup(w http.ResponseWriter, r *http.Request) {
	g, err := h.c.Store.GetGroup(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h.groupResource(g))
}

// memberIDs keeps only members that are real users.
func (h *Handler) memberIDs(r *http.Request, in []scimMember) []string {
	var out []string
	for _, m := range in {
		if _, err := h.c.Store.GetUser(r.Context(), m.Value); err == nil {
			out = append(out, m.Value)
		}
	}
	return out
}

func (h *Handler) createGroup(w http.ResponseWriter, r *http.Request) {
	var in scimGroup
	if !decode(w, r, &in) {
		return
	}
	name := core.SanitizeName(in.DisplayName)
	if in.DisplayName == "" || name == "" {
		writeError(w, http.StatusBadRequest, "invalidValue", "displayName is required")
		return
	}
	g := &store.Group{ID: secrets.RandomID(), Name: name, Description: "Provisioned by SCIM", Source: sourceGroups, CreatedAt: store.Now(), Members: h.memberIDs(r, in.Members)}
	if err := h.c.Store.CreateGroup(r.Context(), g); err != nil {
		h.fail(w, err)
		return
	}
	h.c.Audit(r.Context(), core.Actor{ID: "scim", Name: "scim"}, "group.create", "group", g.ID, g.Name, map[string]string{"via": "scim"})
	h.c.Coord.Trigger()
	writeJSON(w, http.StatusCreated, h.groupResource(g))
}

// editable: SCIM only manages groups it created.
func (h *Handler) editable(w http.ResponseWriter, g *store.Group) bool {
	if g.Source != sourceGroups {
		writeError(w, http.StatusForbidden, "", "this group is managed in the Gorget console, not by SCIM")
		return false
	}
	return true
}

func (h *Handler) replaceGroup(w http.ResponseWriter, r *http.Request) {
	g, err := h.c.Store.GetGroup(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	if !h.editable(w, g) {
		return
	}
	var in scimGroup
	if !decode(w, r, &in) {
		return
	}
	if name := core.SanitizeName(in.DisplayName); in.DisplayName != "" && name != "" {
		g.Name = name
	}
	g.Members = h.memberIDs(r, in.Members)
	h.saveGroup(w, r, g)
}

func (h *Handler) saveGroup(w http.ResponseWriter, r *http.Request, g *store.Group) {
	if err := h.c.Store.UpdateGroup(r.Context(), g); err != nil {
		h.fail(w, err)
		return
	}
	h.c.Audit(r.Context(), core.Actor{ID: "scim", Name: "scim"}, "group.update", "group", g.ID, g.Name, map[string]any{"via": "scim", "members": len(g.Members)})
	h.c.Coord.Trigger()
	writeJSON(w, http.StatusOK, h.groupResource(g))
}

func (h *Handler) patchGroup(w http.ResponseWriter, r *http.Request) {
	g, err := h.c.Store.GetGroup(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	if !h.editable(w, g) {
		return
	}
	var body patchBody
	if !decode(w, r, &body) {
		return
	}
	members := map[string]bool{}
	for _, m := range g.Members {
		members[m] = true
	}
	for _, op := range body.Operations {
		kind := strings.ToLower(op.Op)
		path := strings.ToLower(op.Path)
		switch {
		case path == "displayname" && kind != "remove":
			if n := core.SanitizeName(stringValue(op.Value)); n != "" {
				g.Name = n
			}
		case strings.HasPrefix(path, "members") || path == "":
			ids := h.memberIDs(r, parseMembers(op.Value))
			// members[value eq "id"] removes one member.
			if kind == "remove" {
				if i := strings.Index(op.Path, `"`); i >= 0 {
					if j := strings.LastIndex(op.Path, `"`); j > i {
						delete(members, op.Path[i+1:j])
					}
				} else if len(ids) == 0 {
					members = map[string]bool{} // remove with no target clears the group
				}
				for _, id := range ids {
					delete(members, id)
				}
				continue
			}
			if kind == "replace" && path != "" {
				members = map[string]bool{}
			}
			for _, id := range ids {
				members[id] = true
			}
		}
	}
	g.Members = g.Members[:0]
	for id := range members {
		g.Members = append(g.Members, id)
	}
	h.saveGroup(w, r, g)
}

// parseMembers reads either a list of members or an object that holds one ({"members": [...]}).
func parseMembers(raw json.RawMessage) []scimMember {
	var list []scimMember
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	var obj struct {
		Members []scimMember `json:"members"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return obj.Members
	}
	return nil
}

func (h *Handler) deleteGroup(w http.ResponseWriter, r *http.Request) {
	g, err := h.c.Store.GetGroup(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	if !h.editable(w, g) {
		return
	}
	if err := h.c.Store.DeleteGroup(r.Context(), g.ID); err != nil {
		h.fail(w, err)
		return
	}
	h.c.Audit(r.Context(), core.Actor{ID: "scim", Name: "scim"}, "group.delete", "group", g.ID, g.Name, map[string]string{"via": "scim"})
	h.c.Coord.Trigger()
	w.WriteHeader(http.StatusNoContent)
}

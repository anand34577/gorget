package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/anand34577/gorget/internal/policy"
	"github.com/anand34577/gorget/internal/secrets"
	"github.com/anand34577/gorget/internal/store"
)

// Limits for time-limited access.
const (
	MinAccessMinutes = 5
	MaxAccessMinutes = 7 * 24 * 60
)

// AccessInput is a request for (or grant of) temporary access.
type AccessInput struct {
	Target  string
	Ports   string
	Reason  string
	Minutes int
}

func (c *Core) validateAccess(in *AccessInput) error {
	in.Target = strings.TrimSpace(in.Target)
	in.Ports = strings.TrimSpace(in.Ports)
	if in.Ports == "" {
		in.Ports = "*"
	}
	if in.Minutes < MinAccessMinutes || in.Minutes > MaxAccessMinutes {
		return invalid("access can last between %d minutes and 7 days", MinAccessMinutes)
	}
	if len(in.Reason) > 500 {
		return invalid("the reason is too long")
	}
	if in.Ports != "*" {
		if _, err := policy.ParsePorts(in.Ports); err != nil {
			return invalid("ports: %v", err)
		}
	}
	s := c.Coord.Snapshot()
	switch {
	case strings.HasPrefix(in.Target, "device:"):
		name := strings.TrimPrefix(in.Target, "device:")
		found := false
		for _, d := range s.Devices {
			if d.Name == name && d.Kind != store.KindGateway {
				found = true
			}
		}
		if !found {
			return invalid("there is no device named %q", name)
		}
	case strings.HasPrefix(in.Target, "tag:"):
		found := false
		for _, d := range s.Devices {
			for _, t := range d.Tags {
				if t == in.Target {
					found = true
				}
			}
		}
		if !found {
			return invalid("no device has the tag %q", in.Target)
		}
	default:
		return invalid("choose a device (device:name) or a tag (tag:name)")
	}
	return nil
}

// RequestAccess records a person's request; an administrator decides on it later.
func (c *Core) RequestAccess(ctx context.Context, u *store.User, in AccessInput) (*store.AccessRequest, error) {
	if err := c.validateAccess(&in); err != nil {
		return nil, err
	}
	r := &store.AccessRequest{
		ID: secrets.RandomID(), RequesterID: u.ID, Requester: u.Email, Target: in.Target, Ports: in.Ports,
		Reason: strings.TrimSpace(in.Reason), Minutes: in.Minutes, Status: store.AccessPending, CreatedAt: store.Now(),
	}
	if err := c.Store.CreateAccessRequest(ctx, r); err != nil {
		return nil, err
	}
	c.Audit(ctx, Actor{ID: u.ID, Name: u.Email}, "access.request", "access", r.ID, r.Target, map[string]any{"ports": r.Ports, "minutes": r.Minutes, "reason": r.Reason})
	c.Bus.Publish(EvAccessRequested, map[string]any{"id": r.ID, "requester": r.Requester, "target": r.Target, "ports": r.Ports, "minutes": r.Minutes, "reason": r.Reason})
	return r, nil
}

// GrantAccess lets an administrator give a person access directly.
func (c *Core) GrantAccess(ctx context.Context, by Actor, to *store.User, in AccessInput) (*store.AccessRequest, error) {
	if err := c.validateAccess(&in); err != nil {
		return nil, err
	}
	now := store.Now()
	r := &store.AccessRequest{
		ID: secrets.RandomID(), RequesterID: to.ID, Requester: to.Email, Target: in.Target, Ports: in.Ports,
		Reason: strings.TrimSpace(in.Reason), Minutes: in.Minutes, Status: store.AccessApproved,
		DecidedBy: by.Name, DecidedAt: now, GrantedUntil: now + int64(in.Minutes)*60, CreatedAt: now,
	}
	if err := c.Store.CreateAccessRequest(ctx, r); err != nil {
		return nil, err
	}
	c.Audit(ctx, by, "access.grant", "access", r.ID, r.Target, map[string]any{"user": to.Email, "ports": r.Ports, "minutes": r.Minutes})
	c.Bus.Publish(EvAccessApproved, map[string]any{"id": r.ID, "requester": r.Requester, "target": r.Target, "until": r.GrantedUntil, "by": by.Name})
	c.Coord.Trigger()
	return r, nil
}

// DecideAccess approves, denies or revokes a request. minutes (optional) overrides the requested duration.
func (c *Core) DecideAccess(ctx context.Context, by Actor, id, decision string, minutes int) (*store.AccessRequest, error) {
	r, err := c.Store.GetAccessRequest(ctx, id)
	if err != nil {
		return nil, err
	}
	now := store.Now()
	switch decision {
	case "approve":
		if r.Status != store.AccessPending {
			return nil, invalid("this request was already decided")
		}
		if minutes == 0 {
			minutes = r.Minutes
		}
		if minutes < MinAccessMinutes || minutes > MaxAccessMinutes {
			return nil, invalid("access can last between %d minutes and 7 days", MinAccessMinutes)
		}
		// The target may have disappeared since the request was made.
		in := AccessInput{Target: r.Target, Ports: r.Ports, Minutes: minutes}
		if err := c.validateAccess(&in); err != nil {
			return nil, err
		}
		r.Status, r.Minutes, r.GrantedUntil = store.AccessApproved, minutes, now+int64(minutes)*60
	case "deny":
		if r.Status != store.AccessPending {
			return nil, invalid("this request was already decided")
		}
		r.Status = store.AccessDenied
	case "revoke":
		if r.Status != store.AccessApproved {
			return nil, invalid("only approved access can be revoked")
		}
		r.Status, r.GrantedUntil = store.AccessRevoked, now
	default:
		return nil, invalid("unknown decision %q", decision)
	}
	r.DecidedBy, r.DecidedAt = by.Name, now
	if err := c.Store.UpdateAccessRequest(ctx, r); err != nil {
		return nil, err
	}
	c.Audit(ctx, by, "access."+decision, "access", r.ID, r.Target, map[string]any{"requester": r.Requester, "minutes": r.Minutes})
	ev := EvAccessApproved
	if decision != "approve" {
		ev = EvAccessDenied
	}
	c.Bus.Publish(ev, map[string]any{"id": r.ID, "requester": r.Requester, "target": r.Target, "decision": decision, "until": r.GrantedUntil, "by": by.Name})
	c.Coord.Trigger()
	return r, nil
}

// accessRules turns active grants into policy rules that expire on their own.
func accessRules(grants []store.AccessRequest) []policy.Rule {
	out := make([]policy.Rule, 0, len(grants))
	for _, g := range grants {
		out = append(out, policy.Rule{
			ID:          "access-" + g.ID,
			Description: fmt.Sprintf("Temporary access for %s (%s)", g.Requester, orDash(g.Reason)),
			Action:      "accept",
			Src:         []string{g.Requester},
			Dst:         []string{g.Target + ":" + g.Ports},
			Expires:     time.Unix(g.GrantedUntil, 0).UTC().Format(time.RFC3339),
		})
	}
	return out
}

func orDash(s string) string {
	if s == "" {
		return "no reason given"
	}
	return s
}

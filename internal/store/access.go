package store

import "context"

// Access request states.
const (
	AccessPending  = "pending"
	AccessApproved = "approved"
	AccessDenied   = "denied"
	AccessRevoked  = "revoked"
)

// AccessRequest is a person's request for temporary access to a device or group of
// devices, or an access grant an administrator gave directly. Approved requests
// become time-limited policy rules until they expire or are revoked.
type AccessRequest struct {
	ID           string `db:"id" json:"id"`
	RequesterID  string `db:"requester_id" json:"requester_id"`
	Requester    string `db:"requester" json:"requester"` // email, kept for the history
	Target       string `db:"target" json:"target"`       // device:<name> or tag:<name>
	Ports        string `db:"ports" json:"ports"`         // "22,443", "8000-9000" or "*"
	Reason       string `db:"reason" json:"reason"`
	Minutes      int    `db:"minutes" json:"minutes"` // how long access lasts once approved
	Status       string `db:"status" json:"status"`
	DecidedBy    string `db:"decided_by" json:"decided_by"`
	DecidedAt    int64  `db:"decided_at" json:"decided_at"`
	GrantedUntil int64  `db:"granted_until" json:"granted_until"` // end of access (0 until approved)
	CreatedAt    int64  `db:"created_at" json:"created_at"`
}

func (s *Store) CreateAccessRequest(ctx context.Context, r *AccessRequest) error {
	return s.insert(ctx, s.db, "access_requests", r)
}

func (s *Store) UpdateAccessRequest(ctx context.Context, r *AccessRequest) error {
	return s.update(ctx, s.db, "access_requests", "id", r)
}

func (s *Store) GetAccessRequest(ctx context.Context, id string) (*AccessRequest, error) {
	var r AccessRequest
	return &r, s.get(ctx, s.db, &r, `SELECT * FROM access_requests WHERE id = ?`, id)
}

// ListAccessRequests returns newest first; userID "" lists everyone's.
func (s *Store) ListAccessRequests(ctx context.Context, userID string, limit int) ([]AccessRequest, error) {
	var out []AccessRequest
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	if userID == "" {
		return out, s.sel(ctx, s.db, &out, `SELECT * FROM access_requests ORDER BY created_at DESC LIMIT ?`, limit)
	}
	return out, s.sel(ctx, s.db, &out, `SELECT * FROM access_requests WHERE requester_id = ? ORDER BY created_at DESC LIMIT ?`, userID, limit)
}

// ActiveAccessGrants returns approved requests that are still in force at time now.
func (s *Store) ActiveAccessGrants(ctx context.Context, now int64) ([]AccessRequest, error) {
	var out []AccessRequest
	return out, s.sel(ctx, s.db, &out, `SELECT * FROM access_requests WHERE status = 'approved' AND granted_until > ? ORDER BY created_at`, now)
}

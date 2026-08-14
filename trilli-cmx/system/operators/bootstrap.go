package operators

import (
	"context"
	"fmt"
)

// Bootstrap provisions the first Global admin, or promotes/creates one
// idempotently. Used by the `create-operator` CLI subcommand to seed CMX before
// any UI exists (the operator-equivalent of the app's `create-super`).
//
// The created operator has NO 2FA yet; mandatory enrollment happens on first
// login (SPEC §6.9), where the login flow detects the missing TOTP and forces
// the enroll stage before issuing a session.
func (s *Service) Bootstrap(ctx context.Context, email, name, password string, role Role) (*Operator, error) {
	email = normalizeEmail(email)
	if !role.Valid() {
		return nil, fmt.Errorf("operators: invalid role %q", role)
	}
	if existing, err := s.GetByEmail(ctx, email); err == nil {
		return existing, fmt.Errorf("operators: an operator with email %q already exists (id=%d)", email, existing.ID)
	}
	return s.Create(ctx, CreateInput{
		Email:    email,
		Name:     name,
		Password: password,
		Role:     role,
	})
}

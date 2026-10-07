package auth

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

func readPersonalProfile(ctx context.Context, tx pgx.Tx, actor User) (AccessUser, error) {
	profile, err := scanAccessUser(tx.QueryRow(ctx, "SELECT "+accessUserColumns+accessUserJoin+" WHERE u.id=$1 AND u.tenant_id=$2 AND NOT u.disabled", actor.ID, actor.tenant))
	if err != nil {
		return profile, err
	}
	var exists bool
	err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT FROM adtr.profile_avatars WHERE user_id=$1 AND tenant_id=$2)", actor.ID, actor.tenant).Scan(&exists)
	if exists {
		profile.Avatar = "/api/profile/avatar"
	}
	return profile, err
}

func readPersonalAvatar(ctx context.Context, tx pgx.Tx, actor User) ([]byte, error) {
	var png []byte
	err := tx.QueryRow(ctx, "SELECT png FROM adtr.profile_avatars WHERE user_id=$1 AND tenant_id=$2", actor.ID, actor.tenant).Scan(&png)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fail(404, "avatar_not_found")
	}
	return png, err
}

func (s *Service) savePersonalAvatar(ctx context.Context, tx pgx.Tx, actor User, png []byte) error {
	// A conflicting tenant cannot overwrite an existing avatar, even if storage
	// has been corrupted out of band. Actor identity is never taken from input.
	tag, err := tx.Exec(ctx, `INSERT INTO adtr.profile_avatars(user_id,tenant_id,png,updated_at)
 VALUES($1,$2,$3,$4) ON CONFLICT(user_id) DO UPDATE SET png=EXCLUDED.png,updated_at=EXCLUDED.updated_at
 WHERE adtr.profile_avatars.tenant_id=EXCLUDED.tenant_id`, actor.ID, actor.tenant, png, s.now().UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fail(409, "profile_conflict")
	}
	return s.audit(ctx, tx, actor, "profile_avatar_update", actor.ID)
}

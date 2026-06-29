package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/px0-ai/px0/internal/db"
	"github.com/px0-ai/px0/internal/model"
)

func CreateUser(ctx context.Context, email, passwordHash string) (*model.User, error) {
	u := &model.User{}
	err := db.Pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash) VALUES ($1, $2)
		 RETURNING id, email, password_hash, is_admin, is_verified, created_at`,
		email, passwordHash,
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.IsAdmin, &u.IsVerified, &u.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrDuplicate
		}
		return nil, fmt.Errorf("create user: %w", err)
	}
	return u, nil
}

func CreateVerifiedUser(ctx context.Context, email, passwordHash string) (*model.User, error) {
	u := &model.User{}
	err := db.Pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, is_verified) VALUES ($1, $2, TRUE)
		 RETURNING id, email, password_hash, is_admin, is_verified, created_at`,
		email, passwordHash,
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.IsAdmin, &u.IsVerified, &u.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrDuplicate
		}
		return nil, fmt.Errorf("create verified user: %w", err)
	}
	return u, nil
}

func CreateAdminUser(ctx context.Context, email, passwordHash string, isVerified bool) (*model.User, error) {
	u := &model.User{}
	err := db.Pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, is_admin, is_verified) VALUES ($1, $2, TRUE, $3)
		 RETURNING id, email, password_hash, is_admin, is_verified, created_at`,
		email, passwordHash, isVerified,
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.IsAdmin, &u.IsVerified, &u.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrDuplicate
		}
		return nil, fmt.Errorf("create admin user: %w", err)
	}
	return u, nil
}

func GetUserByEmail(ctx context.Context, email string) (*model.User, error) {
	u := &model.User{}
	err := db.Pool.QueryRow(ctx,
		`SELECT id, email, password_hash, is_admin, is_verified, created_at FROM users WHERE email = $1`,
		email,
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.IsAdmin, &u.IsVerified, &u.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get user by email: %w", err)
	}
	return u, nil
}

func GetUserByID(ctx context.Context, id uuid.UUID) (*model.User, error) {
	u := &model.User{}
	err := db.Pool.QueryRow(ctx,
		`SELECT id, email, password_hash, is_admin, is_verified, created_at FROM users WHERE id = $1`,
		id,
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.IsAdmin, &u.IsVerified, &u.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get user by id: %w", err)
	}
	return u, nil
}

func CreateUserVerification(ctx context.Context, userID uuid.UUID, code string, expiresAt time.Time) error {
	_, err := db.Pool.Exec(ctx,
		`INSERT INTO user_verifications (user_id, code, expires_at) VALUES ($1, $2, $3)`,
		userID, code, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("create user verification: %w", err)
	}
	return nil
}

func GetLatestVerificationCode(ctx context.Context, userID uuid.UUID) (string, time.Time, error) {
	var code string
	var expiresAt time.Time
	err := db.Pool.QueryRow(ctx,
		`SELECT code, expires_at FROM user_verifications WHERE user_id = $1 ORDER BY created_at DESC LIMIT 1`,
		userID,
	).Scan(&code, &expiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", time.Time{}, ErrNotFound
		}
		return "", time.Time{}, fmt.Errorf("get latest verification code: %w", err)
	}
	return code, expiresAt, nil
}

func VerifyUser(ctx context.Context, userID uuid.UUID) error {
	_, err := db.Pool.Exec(ctx,
		`UPDATE users SET is_verified = TRUE WHERE id = $1`,
		userID,
	)
	if err != nil {
		return fmt.Errorf("verify user: %w", err)
	}
	return nil
}

func DeleteUserVerifications(ctx context.Context, userID uuid.UUID) error {
	_, err := db.Pool.Exec(ctx,
		`DELETE FROM user_verifications WHERE user_id = $1`,
		userID,
	)
	if err != nil {
		return fmt.Errorf("delete user verifications: %w", err)
	}
	return nil
}

func CreatePasswordReset(ctx context.Context, userID uuid.UUID, code string, expiresAt time.Time) error {
	_, err := db.Pool.Exec(ctx,
		`INSERT INTO password_resets (user_id, code, expires_at) VALUES ($1, $2, $3)`,
		userID, code, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("create password reset: %w", err)
	}
	return nil
}

func GetPasswordResetByCode(ctx context.Context, code string) (uuid.UUID, time.Time, error) {
	var userID uuid.UUID
	var expiresAt time.Time
	err := db.Pool.QueryRow(ctx,
		`SELECT user_id, expires_at FROM password_resets WHERE code = $1`,
		code,
	).Scan(&userID, &expiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, time.Time{}, ErrNotFound
		}
		return uuid.Nil, time.Time{}, fmt.Errorf("get password reset by code: %w", err)
	}
	return userID, expiresAt, nil
}

func UpdateUserPassword(ctx context.Context, userID uuid.UUID, passwordHash string) error {
	_, err := db.Pool.Exec(ctx,
		`UPDATE users SET password_hash = $1 WHERE id = $2`,
		passwordHash, userID,
	)
	if err != nil {
		return fmt.Errorf("update user password: %w", err)
	}
	return nil
}

func DeletePasswordReset(ctx context.Context, code string) error {
	_, err := db.Pool.Exec(ctx,
		`DELETE FROM password_resets WHERE code = $1`,
		code,
	)
	if err != nil {
		return fmt.Errorf("delete password reset: %w", err)
	}
	return nil
}

func DeleteUserPasswordResets(ctx context.Context, userID uuid.UUID) error {
	_, err := db.Pool.Exec(ctx,
		`DELETE FROM password_resets WHERE user_id = $1`,
		userID,
	)
	if err != nil {
		return fmt.Errorf("delete user password resets: %w", err)
	}
	return nil
}

func UpdateUserEmail(ctx context.Context, id uuid.UUID, email string) error {
	_, err := db.Pool.Exec(ctx,
		`UPDATE users SET email = $1 WHERE id = $2`,
		email, id,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrDuplicate
		}
		return fmt.Errorf("update user email: %w", err)
	}
	return nil
}

func DeleteUser(ctx context.Context, id uuid.UUID) error {
	_, err := db.Pool.Exec(ctx,
		`DELETE FROM users WHERE id = $1`,
		id,
	)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	return nil
}

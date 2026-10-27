package users

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	"github.com/fachschaftinformatik/web/internal/database"
	"github.com/fachschaftinformatik/web/internal/publicid"
)

// CreateUserTx allocates an internal key that cannot reuse a retired account's
// identity. The account and its permanent public ID share the caller's transaction.
func CreateUserTx(ctx context.Context, tx *sql.Tx, params database.CreateUserParams) (database.User, error) {
	userID, err := publicid.NextInteger(ctx, tx, "users")
	if err != nil {
		return database.User{}, err
	}
	params.ID = userID
	params.Email = strings.ToLower(strings.TrimSpace(params.Email))
	user, err := database.New(tx).CreateUser(ctx, params)
	if err != nil {
		return database.User{}, err
	}
	if _, err := publicid.Ensure(ctx, tx, "users", strconv.FormatInt(userID, 10)); err != nil {
		return database.User{}, err
	}
	return user, nil
}

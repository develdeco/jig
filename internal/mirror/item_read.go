package mirror

import (
	"context"

	"github.com/develdeco/jig/internal/mirror/github"
)

// ReadItem reads the item number of repo (owner/name) once, through d's
// client or, with none, one built from the token `gh auth token` prints. It
// reads nothing of the store and writes nothing anywhere: the item is data for
// a caller that only shows it. The refusals are the client's: an item GitHub
// does not have is ITEM_NOT_FOUND, and a missing or logged-out gh is
// GH_NOT_INSTALLED.
func ReadItem(ctx context.Context, d Deps, repo string, number int) (github.ItemDetail, error) {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return github.ItemDetail{}, err
	}
	client, err := d.client()
	if err != nil {
		return github.ItemDetail{}, err
	}
	return client.ItemByNumber(ctx, owner, name, number)
}

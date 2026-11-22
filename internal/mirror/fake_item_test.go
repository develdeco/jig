package mirror

import (
	"context"
	"fmt"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/mirror/github"
)

// ItemByNumber reads a seeded item: c.items holds what GitHub would answer
// by number, and a number with no entry is ITEM_NOT_FOUND, as a pull request
// or a missing number is.
func (c *fakeClient) ItemByNumber(_ context.Context, owner, repo string, number int) (github.ItemDetail, error) {
	c.read("ItemByNumber")
	if d, ok := c.items[number]; ok {
		if d.Repo == "" {
			d.Repo = owner + "/" + repo
		}
		return d, nil
	}
	return github.ItemDetail{}, &axi.Error{Msg: fmt.Sprintf("%s/%s#%d is not an issue this token can read", owner, repo, number), Code: "ITEM_NOT_FOUND"}
}

func (c *prFakeClient) ItemByNumber(context.Context, string, string, int) (github.ItemDetail, error) {
	return github.ItemDetail{}, nil
}

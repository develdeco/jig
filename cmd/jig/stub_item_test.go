package main

import (
	"context"
	"strconv"

	"github.com/develdeco/jig/internal/mirror/github"
)

// ItemByNumber answers an open item titled issueTitle for any number.
func (s *stubGitHub) ItemByNumber(_ context.Context, _, _ string, number int) (github.ItemDetail, error) {
	return github.ItemDetail{Number: number, NodeID: "NODE_ITEM_" + strconv.Itoa(number), Title: s.issueTitle, Open: true}, nil
}

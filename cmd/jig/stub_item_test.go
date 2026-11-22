package main

import (
	"context"
	"strconv"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/mirror/github"
)

// ItemByNumber answers from items when it is set (ITEM_NOT_FOUND for a number
// it lacks), else an open item titled issueTitle for any number.
func (s *stubGitHub) ItemByNumber(_ context.Context, owner, repo string, number int) (github.ItemDetail, error) {
	if s.items != nil {
		d, ok := s.items[number]
		if !ok {
			return github.ItemDetail{}, &axi.Error{Msg: owner + "/" + repo + "#" + strconv.Itoa(number) + " is not an issue this token can read", Code: "ITEM_NOT_FOUND"}
		}
		return d, nil
	}
	return github.ItemDetail{Number: number, NodeID: "NODE_ITEM_" + strconv.Itoa(number), Title: s.issueTitle, Open: true}, nil
}

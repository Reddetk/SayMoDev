// Package midleware
package midleware

import outport "github.com/Reddetk/SayMoDev/identy-service/port/out"

type BlackListChecker struct {
	repo outport.TokenBlacklist
}

func NewBlackListChecker(repo outport.TokenBlacklist) *BlackListChecker {
	return &BlackListChecker{repo}
}

func (bLC *BlackListChecker) IsBlackListed(id string) bool {
	return bLC.repo.IsInRedis(id)
}

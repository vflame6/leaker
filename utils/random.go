package utils

import (
	"math/rand"
)

func PickRandom[T any](v []T, sourceName string, needsKey bool) T {
	var result T
	length := len(v)
	if length == 0 {
		return result
	}
	return v[rand.Intn(length)]
}

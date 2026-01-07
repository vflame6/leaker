package sources

import (
	"github.com/vflame6/leaker/logger"
	"math/rand"
)

func PickRandom[T any](v []T, sourceName string) T {
	var result T
	length := len(v)
	if length == 0 {
		logger.Debugf("Skipping the %s source because there was no API key/secret defined for it.", sourceName)
		return result
	}
	return v[rand.Intn(length)]
}

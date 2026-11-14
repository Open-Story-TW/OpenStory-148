package world

import (
	"strings"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/scripting"
)

func scriptDestination(catalog *content.Catalog, command scripting.Command) (content.Map, content.Portal, error) {
	if command.Kind != "warp" || len(command.Ints) < 1 || len(command.Ints) > 2 || command.Ints[0] < 0 || command.Ints[0] > 999999998 {
		return content.Map{}, content.Portal{}, domain.ErrInvalidInput
	}
	named := len(command.Ints) == 1
	if named {
		if command.Text == "" || len(command.Text) > 64 || strings.ContainsRune(command.Text, '\x00') {
			return content.Map{}, content.Portal{}, domain.ErrInvalidInput
		}
	} else if command.Text != "" || command.Ints[1] < 0 || command.Ints[1] > 255 {
		return content.Map{}, content.Portal{}, domain.ErrInvalidInput
	}
	destination, err := catalog.Map(int(command.Ints[0]))
	if err != nil {
		return content.Map{}, content.Portal{}, err
	}
	for _, portal := range destination.Portals {
		if named && portal.Name == command.Text || !named && portal.ID == int(command.Ints[1]) {
			return destination, portal, nil
		}
	}
	return content.Map{}, content.Portal{}, domain.ErrNotFound
}

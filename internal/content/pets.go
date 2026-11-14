package content

import (
	"fmt"
	"path/filepath"
	"strconv"
	"sync"
)

// PetCommand is one Item.wz/Pet/<id>.img/interact entry.
type PetCommand struct {
	Probability int
	Increase    int
	MinLevel    int
}

// Pet contains the WZ facts used by summon, hunger and command rules.
type Pet struct {
	ID       int
	Name     string // String.wz/Pet.img name, the default pet name
	Hunger   int
	LifeDays int
	Commands []PetCommand
}

var petDefinitions sync.Map

var petNames sync.Map // root -> map[int]string

func petName(root string, id int) string {
	if value, ok := petNames.Load(root); ok {
		return value.(map[int]string)[id]
	}
	names := map[int]string{}
	if node, err := readWZ(filepath.Join(root, "String.wz", "Pet.img.xml")); err == nil {
		for i := range node.Children {
			if n, err := strconv.Atoi(node.Children[i].Name); err == nil {
				names[n] = node.Children[i].stringValue("name", "")
			}
		}
	}
	petNames.Store(root, names)
	return names[id]
}

// PetDefinition reads Item.wz/Pet. Without a WZ root only the Legacy defaults
// remain (hunger 1, no lifetime), and no interaction command is accepted.
func PetDefinition(root string, id int) (Pet, error) {
	if id/10000 != 500 {
		return Pet{}, fmt.Errorf("unsupported pet id %d", id)
	}
	if root == "" {
		return Pet{ID: id, Hunger: 1}, nil
	}
	key := fmt.Sprintf("%s/%d", root, id)
	if value, ok := petDefinitions.Load(key); ok {
		return value.(Pet), nil
	}
	node, err := readWZ(filepath.Join(root, "Item.wz", "Pet", fmt.Sprintf("%d.img.xml", id)))
	if err != nil {
		return Pet{}, err
	}
	info := node.child("info")
	if info == nil {
		return Pet{}, fmt.Errorf("pet %d has no info", id)
	}
	pet := Pet{ID: id, Hunger: max(1, info.intValue("hungry", 1)), LifeDays: max(0, info.intValue("life", 0))}
	if interact := node.child("interact"); interact != nil {
		for i := 0; ; i++ {
			command := interact.child(strconv.Itoa(i))
			if command == nil {
				break
			}
			pet.Commands = append(pet.Commands, PetCommand{Probability: command.intValue("prob", 0), Increase: command.intValue("inc", 0), MinLevel: command.intValue("l0", 1)})
		}
	}
	pet.Name = petName(root, id)
	petDefinitions.Store(key, pet)
	return pet, nil
}

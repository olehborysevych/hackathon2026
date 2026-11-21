package game

import "math/rand/v2"

// Alias parts: "<Adjective> <Animal>". Colour/material adjectives keep the tavern tone and say
// nothing about who sits behind the seat.
var (
	aliasAdj = []string{
		"Amber", "Blue", "Rusty", "Velvet", "Iron", "Copper", "Mossy", "Silver", "Ashen", "Golden",
		"Crimson", "Dusky", "Misty", "Pale", "Stormy", "Sooty", "Brass", "Hazel", "Ivory", "Cobalt",
	}
	aliasAnimal = []string{
		"Owl", "Fox", "Badger", "Moth", "Hare", "Otter", "Wren", "Toad", "Stag", "Newt",
		"Heron", "Lynx", "Crow", "Boar", "Mole", "Finch", "Ferret", "Raven", "Beetle", "Weasel",
	}
)

// pickAliases returns n distinct aliases with distinct adjectives and animals, so no two seats
// look alike at a glance.
func pickAliases(rng *rand.Rand, n int) []string {
	adj := rng.Perm(len(aliasAdj))
	ani := rng.Perm(len(aliasAnimal))
	out := make([]string, n)
	for i := range out {
		out[i] = aliasAdj[adj[i%len(adj)]] + " " + aliasAnimal[ani[i%len(ani)]]
	}
	return out
}

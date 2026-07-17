package openingbook

// seedLines are mainline opening sequences in UCI. They are the fallback book:
// when object storage has none, the engine workers build a book from these and
// upload it, so the platform is never without one. Kept short and mainstream —
// the first several plies of popular openings, where "book" play is uncontroversial.
//
// Weight accrues from overlap: shared early moves (1.e4, 1.d4) appear in many
// lines and so are played more often, which is the correct bias for a book.
var seedLines = [][]string{
	// 1.e4 e5
	{"e2e4", "e7e5", "g1f3", "b8c6", "f1b5", "a7a6", "b5a4", "g8f6", "e1g1", "f8e7"}, // Ruy Lopez
	{"e2e4", "e7e5", "g1f3", "b8c6", "f1c4", "f8c5", "c2c3", "g8f6", "d2d4"},         // Italian
	{"e2e4", "e7e5", "g1f3", "b8c6", "d2d4", "e5d4", "f3d4", "g8f6", "b1c3"},         // Scotch
	{"e2e4", "e7e5", "g1f3", "g8f6", "f3e5", "d7d6", "e5f3", "f6e4"},                 // Petrov
	// 1.e4 c5 — Sicilian
	{"e2e4", "c7c5", "g1f3", "d7d6", "d2d4", "c5d4", "f3d4", "g8f6", "b1c3", "a7a6"}, // Najdorf
	{"e2e4", "c7c5", "g1f3", "b8c6", "d2d4", "c5d4", "f3d4", "g8f6", "b1c3"},         // Classical
	// 1.e4 others
	{"e2e4", "e7e6", "d2d4", "d7d5", "b1c3", "g8f6"},         // French
	{"e2e4", "c7c6", "d2d4", "d7d5", "b1c3", "d5e4", "c3e4"}, // Caro-Kann
	{"e2e4", "d7d5", "e4d5", "d8d5", "b1c3", "d5a5"},         // Scandinavian
	// 1.d4 d5
	{"d2d4", "d7d5", "c2c4", "e7e6", "b1c3", "g8f6", "c1g5", "f8e7"}, // QGD
	{"d2d4", "d7d5", "c2c4", "d5c4", "g1f3", "g8f6", "e2e3"},         // QGA
	{"d2d4", "d7d5", "c2c4", "c7c6", "g1f3", "g8f6", "b1c3"},         // Slav
	{"d2d4", "d7d5", "c1f4", "g8f6", "e2e3"},                         // London
	// 1.d4 Nf6 — Indian defences
	{"d2d4", "g8f6", "c2c4", "g7g6", "b1c3", "f8g7", "e2e4", "d7d6"}, // King's Indian
	{"d2d4", "g8f6", "c2c4", "e7e6", "b1c3", "f8b4"},                 // Nimzo-Indian
	{"d2d4", "g8f6", "c2c4", "e7e6", "g1f3", "b7b6"},                 // Queen's Indian
	// Flank openings
	{"c2c4", "e7e5", "b1c3", "g8f6", "g1f3", "b8c6"}, // English
	{"g1f3", "d7d5", "c2c4", "c7c6", "b2b3"},         // Réti
}

// Seed builds the fallback book from the mainline seed lines. It panics on an
// illegal line, which can only be a programming error in the seed data above and
// is caught by the package tests.
func Seed() *Book {
	b, err := Build(seedLines)
	if err != nil {
		panic("openingbook: seed lines are illegal: " + err.Error())
	}
	return b
}

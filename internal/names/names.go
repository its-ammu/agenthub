// Package names generates memorable, deterministic agent names from session ids.
package names

import (
	"crypto/sha256"
	"fmt"
)

var adjectives = []string{
	"neon", "cosmic", "quantum", "velvet", "turbo", "lunar", "solar", "hyper",
	"glitch", "pixel", "arctic", "ember", "nova", "stellar", "phantom", "cyber",
	"electric", "mystic", "atomic", "rogue", "crimson", "indigo", "jade", "onyx",
	"cobalt", "zephyr", "orbital", "sonic", "frosty", "blazing", "silent", "wild",
	"galactic", "retro", "rapid", "lucid", "savage", "prime", "apex", "vivid",
	"amber", "radiant", "midnight", "feral", "plasma", "static", "ultra", "hollow",
	"molten", "spectral", "turquoise", "iron", "crystal", "thunder", "shadow", "gilded",
	"rustic", "magnetic", "volt", "nimble", "sly", "bold", "daring", "cinder",
}

var nouns = []string{
	"falcon", "axolotl", "nebula", "comet", "panther", "kraken", "phoenix", "lynx",
	"raven", "otter", "wolf", "drake", "mantis", "orca", "jaguar", "sphinx",
	"pulsar", "quasar", "cipher", "vector", "nomad", "ranger", "wraith", "golem",
	"hydra", "viper", "titan", "spark", "echo", "badger", "cobra", "condor",
	"dingo", "fennec", "gecko", "heron", "ibis", "kestrel", "lemur", "manta",
	"narwhal", "ocelot", "pangolin", "quokka", "raptor", "serpent", "tarsier", "umbra",
	"vortex", "walrus", "yeti", "zenith", "atlas", "beacon", "comet", "dynamo",
	"enigma", "flux", "gizmo", "harbor", "isotope", "javelin", "kite", "lantern",
}

// Generate returns a name like "neon-axolotl-7f" derived from the session id.
// attempt > 0 yields a different name for the same session (collision retry).
func Generate(sessionID string, attempt int) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s#%d", sessionID, attempt)))
	return fmt.Sprintf("%s-%s-%02x",
		adjectives[int(h[0])%len(adjectives)],
		nouns[int(h[1])%len(nouns)],
		h[2])
}

package recipes_test

import (
	"testing"

	"github.com/adarshks016/poultice/internal/parse"
	"github.com/adarshks016/poultice/internal/recipe"
	"github.com/adarshks016/poultice/recipes"
)

// Every shipped recipe must load, and must name a parser this build has.
func TestBuiltinRecipesLoad(t *testing.T) {
	rs, errs := recipe.LoadFS(recipes.FS, "builtin")
	for _, err := range errs {
		t.Error(err)
	}
	if len(rs) < 9 {
		t.Errorf("loaded %d built-in recipes, want at least 9", len(rs))
	}
	for _, r := range rs {
		if _, err := parse.Get(r.Diagnose.Parse); err != nil {
			t.Errorf("%s: %v", r.Metadata.Name, err)
		}
	}
}

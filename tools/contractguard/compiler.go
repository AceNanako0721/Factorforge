package contractguard

import (
	"fmt"
	"github.com/dlclark/regexp2"
	js "github.com/santhosh-tekuri/jsonschema/v6"
	"time"
)

type localOnlyLoader struct{}

func (localOnlyLoader) Load(string) (any, error) {
	return nil, fmt.Errorf("unregistered schema resource forbidden")
}

type compatibleRegexp struct {
	expression *regexp2.Regexp
	pattern    string
}

func (r compatibleRegexp) String() string { return r.pattern }
func (r compatibleRegexp) MatchString(value string) bool {
	ok, err := r.expression.MatchString(value)
	return err == nil && ok
}
func NewCompiler() *js.Compiler {
	c := js.NewCompiler()
	c.UseLoader(localOnlyLoader{})
	c.UseRegexpEngine(func(pattern string) (js.Regexp, error) {
		re, err := regexp2.Compile(pattern, regexp2.ECMAScript)
		if err != nil {
			return nil, err
		}
		// A tool execution budget, not a strategy tolerance or production rule.
		re.MatchTimeout = time.Second
		return compatibleRegexp{re, pattern}, nil
	})
	return c
}

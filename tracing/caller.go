package tracing

import (
	"runtime"
	"strings"
)

// callerFuncName returns the caller's function name in the form "StructName.MethodName"
// or "FunctionName" for package-level functions.
// skip is the number of stack frames to skip (0 = caller of callerFuncName).
func callerFuncName(skip int) string {
	pc, _, _, ok := runtime.Caller(skip + 1)
	if !ok {
		return "unknown"
	}
	fn := runtime.FuncForPC(pc)
	if fn == nil {
		return "unknown"
	}
	name := fn.Name()
	// Full name: "github.com/user/project/pkg.(*Struct).Method"
	// Step 1: Strip path prefix → "pkg.(*Struct).Method"
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	// Step 2: Strip package name → "(*Struct).Method"
	if idx := strings.Index(name, "."); idx >= 0 {
		name = name[idx+1:]
	}
	// Step 3: Clean pointer receiver notation "(*Struct)" → "Struct"
	name = strings.NewReplacer("(*", "", ")", "").Replace(name)
	return name
}

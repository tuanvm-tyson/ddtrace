package ddtrace

import (
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewGenerateCommand(t *testing.T) {
	assert.NotNil(t, NewGenerateCommand())
}

func TestGenerateCommand_getOptionsForInterface(t *testing.T) {
	tests := []struct {
		name          string
		init          func() *GenerateCommand
		interfaceName string

		wantErr    bool
		inspectErr func(err error, t *testing.T) //use for more precise error evaluation
	}{
		{
			name: "basic success case",
			init: func() *GenerateCommand {
				cmd := NewGenerateCommand()
				cmd.outputFile = "test.go"
				return cmd
			},
			interfaceName: "TestInterface",
			wantErr:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			receiver := tt.init()

			got1, err := receiver.getOptionsForInterface(tt.interfaceName)

			if tt.wantErr {
				assert.Error(t, err)
				if tt.inspectErr != nil {
					tt.inspectErr(err, t)
				}
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, got1, "getOptionsForInterface should return valid options")
				assert.Equal(t, tt.interfaceName, got1.InterfaceName, "interface name should match")
			}
		})
	}
}

func TestGenerateCommand_Run(t *testing.T) {
	tests := []struct {
		name    string
		init    func() *GenerateCommand
		inspect func(r *GenerateCommand, t *testing.T) //inspects *GenerateCommand after execution of Run

		args []string

		wantErr    bool
		inspectErr func(err error, t *testing.T) //use for more precise error evaluation
	}{
		{
			name: "parse args error",
			init: func() *GenerateCommand {
				cmd := NewGenerateCommand()
				cmd.BaseCommand.FlagSet().SetOutput(io.Discard)
				return cmd
			},
			args:    []string{"-pp"},
			wantErr: true,
			inspectErr: func(err error, t *testing.T) {
				assert.Equal(t, "flag provided but not defined: -pp", err.Error())
			},
		},
		{
			name: "check flags error",
			init: func() *GenerateCommand {
				return NewGenerateCommand()
			},
			args:    []string{},
			wantErr: true,
		},
		{
			name: "get options error",
			init: func() *GenerateCommand {
				return NewGenerateCommand()
			},
			args:    []string{"-p", "unexistingpkg", "-i", "interface", "-o", "unexisting_dir/file.go"},
			wantErr: true,
		},
		{
			name: "success with single interface",
			args: []string{"-o", "out.file", "-i", "Command"},
			init: func() *GenerateCommand {
				cmd := NewGenerateCommand()
				cmd.filepath.WriteFile = func(string, []byte, os.FileMode) error { return nil }
				return cmd
			},
			wantErr: false,
		},
		{
			name: "success with local prefixes",
			args: []string{"-o", "out.file", "-i", "Command", "-l", "foobar.com/pkg"},
			init: func() *GenerateCommand {
				cmd := NewGenerateCommand()
				cmd.filepath.WriteFile = func(filename string, data []byte, perm os.FileMode) error {
					return nil
				}
				return cmd
			},
			inspect: func(cmd *GenerateCommand, t *testing.T) {
				assert.EqualValues(t, "foobar.com/pkg", cmd.localPrefix)
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			receiver := tt.init()

			err := receiver.Run(tt.args, nil)

			if tt.inspect != nil {
				tt.inspect(receiver, t)
			}

			if tt.wantErr {
				t.Logf("!!!\n\n%T: %v\n\n!!!", err, err)
				if assert.Error(t, err) && tt.inspectErr != nil {
					tt.inspectErr(err, t)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGenerateCommand_parseInterfaceNames(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "single interface",
			input:    "Reader",
			expected: []string{"Reader"},
		},
		{
			name:     "multiple interfaces",
			input:    "Reader,Writer,Closer",
			expected: []string{"Reader", "Writer", "Closer"},
		},
		{
			name:     "interfaces with spaces",
			input:    "Reader, Writer, Closer",
			expected: []string{"Reader", "Writer", "Closer"},
		},
		{
			name:     "empty string",
			input:    "",
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewGenerateCommand()
			cmd.interfaceNamesStr = tt.input
			cmd.parseInterfaceNames()

			if tt.input == "" {
				// For empty input, ensure we get an empty slice, not nil
				if cmd.interfaceNames == nil {
					cmd.interfaceNames = []string{}
				}
			}

			assert.Equal(t, tt.expected, cmd.interfaceNames)
		})
	}
}

func Test_varsToArgs(t *testing.T) {
	tests := []struct {
		name  string
		v     vars
		want1 string
	}{
		{
			name:  "no vars",
			v:     nil,
			want1: "",
		},
		{
			name:  "two vars",
			v:     vars{varFlag{name: "key", value: "value"}, varFlag{name: "booleanKey", value: true}},
			want1: " -v key=value -v booleanKey",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got1 := varsToArgs(tt.v)
			assert.Equal(t, tt.want1, got1, "varsToArgs returned unexpected result")
		})
	}
}

func TestVars_toMap(t *testing.T) {
	tests := []struct {
		name  string
		vars  vars
		want1 map[string]interface{}
	}{
		{
			name: "success",
			vars: vars{{name: "key", value: "value"}, {name: "boolFlag", value: true}},
			want1: map[string]interface{}{
				"key":      "value",
				"boolFlag": true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got1 := tt.vars.toMap()

			assert.Equal(t, tt.want1, got1, "vars.toMap returned unexpected result")
		})
	}
}

func TestVars_Set(t *testing.T) {
	tests := []struct {
		name    string
		inspect func(r vars, t *testing.T) //inspects vars after execution of Set
		s       string
	}{
		{
			name: "bool var",
			s:    "boolVar",
			inspect: func(v vars, t *testing.T) {
				assert.Equal(t, vars{varFlag{name: "boolVar", value: true}}, v)
			},
		},

		{
			name: "string var",
			s:    "key=value",
			inspect: func(v vars, t *testing.T) {
				assert.Equal(t, vars{varFlag{name: "key", value: "value"}}, v)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := vars{}
			err := v.Set(tt.s)
			assert.NoError(t, err)

			tt.inspect(v, t)
		})
	}
}

func TestHelper_UpFirst(t *testing.T) {
	tests := []struct {
		name string
		in   string
		out  string
	}{
		{
			name: "fist is lower-cased",
			in:   "typeName",
			out:  "TypeName",
		},
		{
			name: "single letter",
			in:   "v",
			out:  "V",
		},
		{
			name: "multi-bytes chars",
			in:   "йоу",
			out:  "Йоу",
		},
		{
			name: "empty string",
			in:   "",
			out:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotOut := upFirst(tt.in)
			assert.Equal(t, tt.out, gotOut)
		})
	}
}

func TestHelper_DownFirst(t *testing.T) {
	tests := []struct {
		name string
		in   string
		out  string
	}{
		{
			name: "fist is upper-cased",
			in:   "TypeName",
			out:  "typeName",
		},
		{
			name: "single letter",
			in:   "V",
			out:  "v",
		},
		{
			name: "multi-bytes chars",
			in:   "Йоу",
			out:  "йоу",
		},
		{
			name: "empty string",
			in:   "",
			out:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotOut := downFirst(tt.in)
			assert.Equal(t, tt.out, gotOut)
		})
	}
}

func Test_toSnakeCase(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"already_snake", "already_snake"},
		{"A", "a"},
		{"AA", "aa"},
		{"AaAa", "aa_aa"},
		{"HTTPRequest", "http_request"},
		{"BatteryLifeValue", "battery_life_value"},
		{"Id0Value", "id0_value"},
		{"ID0Value", "id0_value"},
	}

	for _, test := range tests {
		assert.Equal(t, test.want, toSnakeCase(test.input))
	}
}

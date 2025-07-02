package ddtrace

import (
	"bytes"
	"flag"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBaseCommand_ShortDescription(t *testing.T) {
	base := BaseCommand{Short: "short"}
	assert.Equal(t, "short", base.ShortDescription())
}

func TestBaseCommand_UsageLine(t *testing.T) {
	base := BaseCommand{Usage: "usage line"}
	assert.Equal(t, "usage line", base.UsageLine())
}

func TestBaseCommand_FlagSet(t *testing.T) {
	base := BaseCommand{}
	assert.Nil(t, base.FlagSet())
}

func TestBaseCommand_HelpMessage(t *testing.T) {
	type args struct {
		w io.Writer
	}
	tests := []struct {
		name        string
		init        func() BaseCommand
		args        func() args
		wantErr     bool
		wantContent string
	}{
		{
			name: "success",
			init: func() BaseCommand {
				return BaseCommand{Help: "help"}
			},
			args: func() args {
				return args{w: &bytes.Buffer{}}
			},
			wantErr:     false,
			wantContent: "help",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tArgs := tt.args()
			receiver := tt.init()

			err := receiver.HelpMessage(tArgs.w)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				if buf, ok := tArgs.w.(*bytes.Buffer); ok {
					assert.Equal(t, tt.wantContent, buf.String())
				}
			}
		})
	}
}

func TestRegisterCommand(t *testing.T) {
	cmd := &GenerateCommand{BaseCommand: BaseCommand{Flags: flag.NewFlagSet("flagset", flag.ContinueOnError)}}
	RegisterCommand("TestRegisterCommand", cmd)
	assert.NotNil(t, commands["TestRegisterCommand"])
}

func TestGetCommand(t *testing.T) {
	commands["TestGetCommand"] = &GenerateCommand{}
	assert.NotNil(t, GetCommand("TestGetCommand"))
}

func TestUsage(t *testing.T) {
	w := &bytes.Buffer{}
	assert.NoError(t, Usage(w))
	assert.NotEmpty(t, w.String())
}

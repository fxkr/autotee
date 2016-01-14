package autotee

import (
	"bytes"
	"strconv"

	"github.com/juju/errors"
	"github.com/mattn/go-shellwords"
)

type CmdData struct {
	Name string
	Args []string
}

func NewCmdData(line string) (cd CmdData, err error) {
	args, err := shellwords.Parse(line)
	if err != nil {
		err = errors.Trace(err)
		return
	}

	if len(args) == 0 || len(args[0]) == 0 {
		err = errors.Errorf("Command must not be empty: %#v", args)
		return
	}

	return CmdData{args[0], args[1:]}, nil
}

func (cd *CmdData) Replace(replacements map[string]string) (result CmdData) {
	result.Name = cd.Name
	result.Args = make([]string, len(cd.Args))
	for i, _ := range cd.Args {
		result.Args[i] = cd.Args[i]
		for old, new := range replacements {
			if result.Args[i] == old {
				result.Args[i] = new
				break
			}
		}
	}
	return
}

func (cd *CmdData) NewCmd() *Cmd {
	return Command(cd.Name, cd.Args...)
}

func (cd *CmdData) Equals(other CmdData) bool {
	if cd.Name != other.Name {
		return false
	}
	if cd.Args == nil && other.Args == nil {
		return true
	}
	if cd.Args == nil || other.Args == nil {
		return false
	}
	if len(cd.Args) != len(other.Args) {
		return false
	}
	for i := 0; i < len(cd.Args); i++ {
		if cd.Args[i] != other.Args[i] {
			return false
		}
	}
	return true
}

func (cd *CmdData) String() string {
	var buffer bytes.Buffer

	buffer.WriteString(strconv.Quote(cd.Name))
	for _, arg := range cd.Args {
		buffer.WriteString(" ")
		buffer.WriteString(strconv.Quote(arg))
	}

	return buffer.String()
}

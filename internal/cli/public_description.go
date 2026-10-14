package cli

import (
	"fmt"
	"strings"

	"github.com/kunchenguid/no-mistakes/internal/ipc"
	"github.com/kunchenguid/no-mistakes/internal/publicdescription"
)

const publicDescriptionPushOptionPrefix = "no-mistakes.public-description="

func parsePublicDescriptionPushOptions(options []string) (string, error) {
	p := ""
	for _, option := range options {
		value, ok := strings.CutPrefix(option, publicDescriptionPushOptionPrefix)
		if !ok {
			continue
		}
		if p != "" {
			return "", fmt.Errorf("duplicate public description push option")
		}
		if err := publicdescription.ValidatePath(value); err != nil {
			return "", err
		}
		p = value
	}
	return p, nil
}

func requireDaemonPublicDescription(client *ipc.Client, repoID, requested string) error {
	if requested == "" {
		return nil
	}
	var result ipc.ProbeOmitIntentResult
	if err := client.Call(ipc.MethodProbePublicDescription, &ipc.ProbePublicDescriptionParams{RepoID: repoID}, &result); err != nil || !result.OK {
		return fmt.Errorf("running daemon/gate notification hook does not support --public-description; refusing to start or push (activate compatible daemon and gate hook before use)")
	}
	return nil
}

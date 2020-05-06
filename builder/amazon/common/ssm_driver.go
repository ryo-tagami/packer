package common

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"

	"github.com/aws/aws-sdk-go/service/ssm"
)

const sessionManagerPluginName string = "session-manager-plugin"

//sessionCommand is the AWS-SDK equivalent to the command you would specify to `aws ssm ...`
const sessionCommand string = "StartSession"

type SSMDriver struct {
	Region          string
	ProfileName     string
	Session         *ssm.StartSessionOutput
	SessionParams   ssm.StartSessionInput
	SessionEndpoint string
	// Provided for testing purposes; if not specified it defaults to sessionManagerPluginName
	PluginName string
}

// StartSession starts an interactive Systems Manager session with a remote instance via the AWS session-manager-plugin
func (sd *SSMDriver) StartSession(ctx context.Context) error {
	if sd.PluginName == "" {
		sd.PluginName = sessionManagerPluginName
	}

	args, err := sd.Args()
	if err != nil {
		err = fmt.Errorf("error encountered validating session details: %s", err)
		return err
	}

	cmd := exec.CommandContext(ctx, sd.PluginName, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	// Aggregate all output into one reader
	combinedOut := io.MultiReader(stdout, stderr)

	if err := cmd.Start(); err != nil {
		err = fmt.Errorf("error encountered when calling %s: %s\n", sd.PluginName, err)
		return err
	}

	output := bufio.NewScanner(combinedOut)
	successLogLine := fmt.Sprintf("opened for sessionId %s", *sd.Session.SessionId)
	for output.Scan() {
		if output.Err() != nil && output.Err() != io.EOF {
			break
		}

		out := output.Text()
		if out != "" {
			if strings.Contains(out, "panic") {
				line := fmt.Sprintf("[%s stderr] %s\n", sd.PluginName, out)
				log.Print(line)
				return fmt.Errorf("exited with a non-zero status")
			}

			line := fmt.Sprintf("[%s] %s\n", sd.PluginName, out)
			log.Print(line)

			if strings.Contains(line, successLogLine) {
				return nil
			}
		}
	}

	// if we get here then something expected happened with the logging.
	return fmt.Errorf("unable to determine if a successful tunnel has been established; giving up")
}

func (sd *SSMDriver) Args() ([]string, error) {
	if sd.Session == nil {
		return nil, fmt.Errorf("an active Amazon SSM Session is required before trying to open a session tunnel")
	}

	// AWS session-manager-plugin requires a valid session be passed in JSON.
	sessionDetails, err := json.Marshal(sd.Session)
	if err != nil {
		return nil, fmt.Errorf("error encountered in reading session details %s", err)
	}

	// AWS session-manager-plugin requires the parameters used in the session to be passed in JSON as well.
	sessionParameters, err := json.Marshal(sd.SessionParams)
	if err != nil {
		return nil, fmt.Errorf("error encountered in reading session parameter details %s", err)
	}

	// Args must be in this order
	args := []string{
		string(sessionDetails),
		sd.Region,
		sessionCommand,
		sd.ProfileName,
		string(sessionParameters),
		sd.SessionEndpoint,
	}

	return args, nil
}

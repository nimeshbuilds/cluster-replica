package testrun

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"

	api "github.com/nimeshbuilds/cluster-replica/api/v1alpha1"
)

type Reference struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	UID  string `json:"uid"`
}

type FaultEvidence struct {
	Kind      string           `json:"kind"`
	Namespace string           `json:"namespace"`
	Target    *api.ChaosTarget `json:"target,omitempty"`
	Image     string           `json:"image,omitempty"`
}

type Outcome struct {
	Status          string  `json:"status"`
	Reason          string  `json:"reason,omitempty"`
	ExitCode        *int    `json:"exitCode,omitempty"`
	DurationSeconds float64 `json:"durationSeconds,omitempty"`
}

// Report contains identities and results only. Never add raw recipes, arbitrary
// condition messages, command lines, captured configuration, credentials or logs.
type Report struct {
	APIVersion       string                `json:"apiVersion"`
	Kind             string                `json:"kind"`
	RunID            string                `json:"runID"`
	Namespace        string                `json:"namespace"`
	StartedAt        time.Time             `json:"startedAt"`
	FinishedAt       time.Time             `json:"finishedAt"`
	RecipeSHA256     string                `json:"recipeSHA256"`
	Request          *Reference            `json:"request,omitempty"`
	Replica          *Reference            `json:"replica,omitempty"`
	PlanRevision     string                `json:"planRevision,omitempty"`
	Resources        []api.PlannedResource `json:"resources,omitempty"`
	CapturedAt       *time.Time            `json:"capturedAt,omitempty"`
	Runtime          *api.RuntimeReference `json:"runtime,omitempty"`
	SourceVersion    string                `json:"sourceVersion,omitempty"`
	TargetVersion    string                `json:"targetVersion,omitempty"`
	MirrorCapture    *Reference            `json:"mirrorCapture,omitempty"`
	MirrorCapturedAt *time.Time            `json:"mirrorCapturedAt,omitempty"`
	Experiment       *Reference            `json:"experiment,omitempty"`
	ChaosFaults      []FaultEvidence       `json:"chaosFaults,omitempty"`
	Setup            Outcome               `json:"setup"`
	Test             Outcome               `json:"test"`
	Cleanup          Outcome               `json:"cleanup"`
	RetainedUntil    *time.Time            `json:"retainedUntil,omitempty"`
}

func (r Report) Successful() bool {
	return r.Setup.Status == "Passed" && r.Test.Status == "Passed" && r.Cleanup.Status == "Verified"
}

func WriteReports(dir string, report Report) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return errors.New("cannot encode run report")
	}
	if err := writeNew(filepath.Join(dir, "report.json"), append(data, '\n')); err != nil {
		return err
	}
	suite := junitSuite{Name: "replicove", Tests: 3}
	for _, entry := range []struct {
		name   string
		result Outcome
	}{
		{"provision", report.Setup}, {"test-command", report.Test}, {"cleanup", report.Cleanup},
	} {
		test := junitCase{Name: entry.name, Classname: "replicove", Time: strconv.FormatFloat(entry.result.DurationSeconds, 'f', 3, 64)}
		switch entry.result.Status {
		case "Passed", "Verified":
		case "NotRun", "NotNeeded":
			suite.Skipped++
			test.Skipped = &junitMessage{Message: entry.result.Reason}
		case "Failed":
			if entry.name == "test-command" {
				suite.Failures++
				test.Failure = &junitMessage{Message: entry.result.Reason}
			} else {
				suite.Errors++
				test.Error = &junitMessage{Message: entry.result.Reason}
			}
		default:
			suite.Errors++
			test.Error = &junitMessage{Message: entry.result.Reason}
		}
		suite.Cases = append(suite.Cases, test)
	}
	data, err = xml.MarshalIndent(suite, "", "  ")
	if err != nil {
		return errors.New("cannot encode JUnit report")
	}
	return writeNew(filepath.Join(dir, "junit.xml"), append([]byte(xml.Header), append(data, '\n')...))
}

func writeNew(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("cannot create report file; existing files are never overwritten")
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		return errors.New("cannot write run report")
	}
	return nil
}

type junitSuite struct {
	XMLName  xml.Name    `xml:"testsuite"`
	Name     string      `xml:"name,attr"`
	Tests    int         `xml:"tests,attr"`
	Failures int         `xml:"failures,attr"`
	Errors   int         `xml:"errors,attr"`
	Skipped  int         `xml:"skipped,attr"`
	Cases    []junitCase `xml:"testcase"`
}
type junitCase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	Time      string        `xml:"time,attr"`
	Failure   *junitMessage `xml:"failure,omitempty"`
	Error     *junitMessage `xml:"error,omitempty"`
	Skipped   *junitMessage `xml:"skipped,omitempty"`
}
type junitMessage struct {
	Message string `xml:"message,attr"`
}

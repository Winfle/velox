package build

import (
	"bytes"
	"math/rand"
	"os"
	"os/exec"
	"path"
	"syscall"

	"github.com/roadrunner-server/velox/shared"
	"go.uber.org/zap"
)

const (
	// path to the file which should be generated from the template
	pluginsPath        string = "/internal/container/plugins.go"
	letterBytes               = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	goModStr           string = "go.mod"
	pluginStructureStr string = "Plugin{}"
	rrMainGo           string = "cmd/rr/main.go"
)

type Builder struct {
	rrPath    string
	out       string
	modules   []*shared.ModulesInfo
	log       *zap.Logger
	buildArgs []string
}

func NewBuilder(rrPath string, modules []*shared.ModulesInfo, out string, log *zap.Logger, buildArgs []string) *Builder {
	return &Builder{
		rrPath:    rrPath,
		modules:   modules,
		buildArgs: buildArgs,
		out:       out,
		log:       log,
	}
}

func (b *Builder) Build() error { //nolint:gocyclo
	t := new(Template)
	t.Entries = make([]*Entry, len(b.modules))
	for i := 0; i < len(b.modules); i++ {
		e := new(Entry)

		e.Module = b.modules[i].ModuleName
		e.Prefix = RandStringBytes(5)
		e.Structure = pluginStructureStr
		e.Version = b.modules[i].Version
		e.Replace = b.modules[i].Replace

		t.Entries[i] = e
	}

	buf := new(bytes.Buffer)
	err := compileTemplate(buf, t)
	if err != nil {
		return err
	}

	f, err := os.Open(b.rrPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = f.Close()
	}()

	// remove old plugins.go
	err = os.Remove(path.Join(b.rrPath, pluginsPath))
	if err != nil {
		return err
	}

	err = os.WriteFile(path.Join(b.rrPath, pluginsPath), buf.Bytes(), os.ModePerm)
	if err != nil {
		return err
	}

	err = os.Remove(path.Join(b.rrPath, goModStr))
	if err != nil {
		return err
	}

	goModFile, err := os.Create(path.Join(b.rrPath, goModStr))
	if err != nil {
		return err
	}

	buf.Reset()

	err = compileGoModTemplate(buf, t)
	if err != nil {
		return err
	}

	_, err = goModFile.Write(buf.Bytes())
	if err != nil {
		return err
	}

	buf.Reset()

	b.log.Info("[SWITCHING WORKING DIR]", zap.String("wd", b.rrPath), zap.String("!!!NOTE!!!", "If you won't specify full path for the binary it'll be in that working dir"))
	err = syscall.Chdir(b.rrPath)
	if err != nil {
		return err
	}

	for i := 0; i < len(t.Entries); i++ {
		// go get only deps w/o replace
		if t.Entries[i].Replace != "" {
			continue
		}
		err = b.goGetMod(t.Entries[i].Module, t.Entries[i].Version)
		if err != nil {
			return err
		}
	}

	// go mod tidy for the old packages
	err = b.goModTidyCmd116()
	if err != nil {
		return err
	}

	// upgrade to 1.17
	err = b.goModTidyCmd117()
	if err != nil {
		return err
	}

	err = b.goBuildCmd(b.out)
	if err != nil {
		return err
	}

	return nil
}

func RandStringBytes(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = letterBytes[rand.Intn(len(letterBytes))] //nolint:gosec
	}
	return string(b)
}

func (b *Builder) goBuildCmd(out string) error {
	var cmd *exec.Cmd
	if len(b.buildArgs) != 0 {
		buildCmdArgs := make([]string, 0, len(b.buildArgs)+5)
		buildCmdArgs = append(buildCmdArgs, "build")
		// verbose
		buildCmdArgs = append(buildCmdArgs, "-v")
		// build args
		buildCmdArgs = append(buildCmdArgs, b.buildArgs...)
		// output file
		buildCmdArgs = append(buildCmdArgs, "-o")
		// path
		buildCmdArgs = append(buildCmdArgs, out)
		// path to main.go
		buildCmdArgs = append(buildCmdArgs, rrMainGo)
		cmd = exec.Command("go", buildCmdArgs...)
	} else {
		cmd = exec.Command("go", "build", "-o", out, rrMainGo)
	}

	b.log.Info("[EXECUTING CMD]", zap.String("cmd", cmd.String()))
	cmd.Stderr = b
	cmd.Stdout = b
	err := cmd.Start()
	if err != nil {
		return err
	}
	err = cmd.Wait()
	if err != nil {
		return err
	}
	return nil
}

func (b *Builder) goModTidyCmd116() error {
	b.log.Info("[EXECUTING CMD]", zap.String("cmd", "go mod tidy -go=1.16"))
	cmd := exec.Command("go", "mod", "tidy", "-go=1.16")
	cmd.Stderr = b
	err := cmd.Start()
	if err != nil {
		return err
	}
	err = cmd.Wait()
	if err != nil {
		return err
	}
	return nil
}

func (b *Builder) goModTidyCmd117() error {
	b.log.Info("[EXECUTING CMD]", zap.String("cmd", "go mod tidy -go=1.17"))
	cmd := exec.Command("go", "mod", "tidy", "-go=1.17")
	cmd.Stderr = b
	err := cmd.Start()
	if err != nil {
		return err
	}
	err = cmd.Wait()
	if err != nil {
		return err
	}
	return nil
}

func (b *Builder) goGetMod(repo, hash string) error {
	b.log.Info("[EXECUTING CMD]", zap.String("cmd", "go get "+repo+"@"+hash))
	cmd := exec.Command("go", "get", repo+"@"+hash) //nolint:gosec
	cmd.Stderr = b

	err := cmd.Start()
	if err != nil {
		return err
	}
	err = cmd.Wait()
	if err != nil {
		return err
	}
	return nil
}

func (b *Builder) Write(d []byte) (int, error) {
	b.log.Debug("[STDERR OUTPUT]", zap.ByteString("log", d))
	return len(d), nil
}

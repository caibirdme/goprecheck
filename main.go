package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
)

type configer struct {
	PackageName string
	FilterRegxp string
	Checkers    []supportedChecker
	Show        bool
	Goroutines  int
}

type supportedChecker struct {
	Command    string
	Args       []string
	FullPath   bool
	Prefix     string
	OnePackage bool
}

var (
	cfg  configer
	rate chan struct{}
)

const (
	defaultConfigFileName = "conf.toml"
)

func init() {
	var confFileName, packageName string
	flag.StringVar(&confFileName, "conf", defaultConfigFileName, "specify config file path")
	flag.StringVar(&packageName, "p", "", "specify packageName ($GOPATH/packageName, don't contain $GOPATH)")
	flag.Parse()
	err := loadConfig(confFileName)
	hE(err)
	if packageName != "" {
		cfg.PackageName = packageName
	}
	rate = make(chan struct{}, cfg.Goroutines)
}

func loadConfig(fileName string) error {
	fd, err := os.Open(fileName)
	if nil != err && err == os.ErrNotExist {
		if fileName == defaultConfigFileName {
			return nil
		}
		return fmt.Errorf("couldn't open config file: %s", err)
	}
	defer fd.Close()
	_, err = toml.DecodeReader(fd, &cfg)
	if nil != err {
		return fmt.Errorf("config file must be a standard toml format: %s", err)
	}
	if cfg.Goroutines <= 1 {
		cfg.Goroutines = 5
	}
	return nil
}

func main() {
	deps, err := getDependencies()
	hE(err)
	if cfg.Show {
		formatOutput(deps)
	}
	errs := doCheck(deps)
	if nil != errs {
		errs.Output()
		os.Exit(1)
	}
	fmt.Println("Excellent!")
}

func formatOutput(deps []string) {
	fmt.Printf("Dependencies in %s:\n", cfg.PackageName)
	for _, dep := range deps {
		fmt.Println(dep)
	}
	fmt.Printf("\n\n")
}

func hE(e error) {
	if e == nil {
		return
	}
	fmt.Println(e)
	os.Exit(1)
}

func getDependencies() ([]string, error) {
	packageName := cfg.PackageName
	command := []string{"go", "list", "-f", "'{{ .Deps }}'", "-json"}
	if packageName != "" {
		command = append(command, packageName)
	}
	var out bytes.Buffer
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdout = &out
	err := cmd.Run()
	if nil != err {
		return nil, err
	}
	var depRes = struct {
		Deps       []string
		ImportPath string
	}{}
	err = json.Unmarshal(out.Bytes(), &depRes)
	if nil != err {
		return nil, err
	}
	if cfg.Show {
		fmt.Printf("ImportPath: %s\n", depRes.ImportPath)
	}
	return filterDependencies(depRes.Deps, depRes.ImportPath), nil
}

type dependencyFilter func(packageName []byte) bool

func getFilter() dependencyFilter {
	if cfg.FilterRegxp == "" {
		return filterNotVendor
	}
	reg, err := regexp.Compile(cfg.FilterRegxp)
	if nil != err {
		return filterNotVendor
	}
	return regexpFilter(reg)
}

func regexpFilter(reg *regexp.Regexp) dependencyFilter {
	return func(packageName []byte) bool {
		return reg.Match(packageName)
	}
}

func filterDependencies(deps []string, packageName string) []string {
	var filtered []string
	filter := getFilter()
	for _, dep := range deps {
		if !strings.HasPrefix(dep, packageName) {
			continue
		}
		if filter([]byte(dep)) {
			filtered = append(filtered, dep)
		}
	}
	return append(filtered, packageName)
}

func filterNotVendor(packageName []byte) bool {
	return !bytes.Contains(packageName, []byte("/vendor/"))
}

func doCheck(deps []string) errSlice {
	if nil == deps || 0 == len(deps) || len(cfg.Checkers) == 0 {
		return nil
	}
	var wg sync.WaitGroup
	var res errSlice
	errorCh := make(chan checkerErr, cfg.Goroutines)
	consumerFinish := make(chan struct{}, 1)
	go func() {
		for e := range errorCh {
			res = append(res, e)
		}
		consumerFinish <- struct{}{}
	}()
	for _, checker := range cfg.Checkers {
		rate <- struct{}{}
		wg.Add(1)
		go runChecker(checker, deps, errorCh, func() {
			<-rate
			wg.Done()
		})
	}
	wg.Wait()
	close(errorCh)
	<-consumerFinish
	return errSlice(res)
}

func runChecker(checker supportedChecker, deps []string, errCh chan<- checkerErr, done func()) {
	if cfg.Show {
		fmt.Printf("Run command: %s %s deps...(%d)\n", checker.Command, strings.Join(checker.Args, " "), len(deps))
	}
	if !checker.OnePackage {
		multiCheckerRuner(checker.Command, append(checker.Args, deps...), errCh)
		done()
		return
	}
	onePackageCheckerRuner(checker.Command, checker.Args, deps, errCh, func() {
		done()
	})
}

//multiCheckerRuner will run the command which support multiple packages as its param
//eg: gosimple unused
//for gosimple,you can run `gosimple packageA packageB ... packageN`
func multiCheckerRuner(command string, args []string, errCh chan<- checkerErr) {
	cmd := exec.Command(command, args...)
	out, err := cmd.CombinedOutput()
	if nil == err {
		return
	}
	if _, ok := err.(*exec.ExitError); !ok {
		fmt.Printf("Run command %s Error: %s", command, err)
		return
	}
	errCh <- checkerErr{command, out}
}

//onePackageCheckerRuner will run the command which support only one package for each invoke
//eg: golint
//for golint,you can't do like `golint packageA packageB`
func onePackageCheckerRuner(command string, args []string, deps []string, errCh chan<- checkerErr, done func()) {
	var wg sync.WaitGroup
	wg.Add(len(deps))
	for _, dep := range deps {
		rate <- struct{}{}
		go func(dep string) {
			defer func() {
				<-rate
				wg.Done()
			}()
			multiCheckerRuner(command, append(args, dep), errCh)
		}(dep)
	}
	wg.Wait()
	done()
}

func addPrefix(deps []string, prefix string) {
	if prefix == "" {
		prefix = os.Getenv("GOPATH")
	}
	if prefix == "" {
		return
	}
	length := len(deps)
	for i := 0; i < length; i++ {
		deps[i] = path.Join(prefix, deps[i])
	}
}

type checkerErr struct {
	command string
	e       []byte
}

type errSlice []checkerErr

func (s errSlice) Len() int {
	return len(s)
}

func (s errSlice) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (s errSlice) Less(i, j int) bool {
	return s[i].command < s[j].command
}

func (s errSlice) Output() {
	sort.Sort(s)
	command := ""
	for _, item := range s {
		if item.command != command {
			command = item.command
			fmt.Printf("\n\n\n---------- %s ---------:\n", command)
		}
		fmt.Println(string(item.e))
	}
}

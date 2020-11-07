/*
reload watches your .exe, .html, .js, etc. files in a directory and
invokes restart if file changes.
*/
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/howeyc/fsnotify"
)

// Milliseconds to wait for the next job to begin after a file change
const WorkDelay = 500

// Default pattern to match files which trigger a reload
const FilePattern = `.+\.tpl|.+\.htm|.+\.html|.+\.js|.+\.css|.+\.yml|.+\.yaml|.+\.exe`

type globList []string

func (g *globList) String() string {
	return fmt.Sprint(*g)
}
func (g *globList) Set(value string) error {
	*g = append(*g, value)
	return nil
}
func (g *globList) Matches(value string) bool {
	for _, v := range *g {
		if match, err := filepath.Match(v, value); err != nil {
			logger.Fatalf("Bad pattern \"%s\": %s", v, err.Error())
		} else if match {
			return true
		}
	}
	return false
}

var (
	flag_pattern   = flag.String("pattern", FilePattern, "Watch all dirs. recursively")
	flag_directory = flag.String("dir", ".", "Directory to watch for changes")
	flag_recursive = flag.Bool("recursive", true, "Watch all dirs. recursively")

	// initialized in main() due to custom type.
	flag_excludedDirs  globList
	flag_excludedFiles globList
	flag_includedFiles globList
)

var logger *log.Logger

func matchesPattern(pattern *regexp.Regexp, file string) bool {
	return pattern.MatchString(file)
}

// Start the supplied command and return stdout and stderr pipes for loging.
func startCommand(command string) (cmd *exec.Cmd, err error) {
	args := strings.Split(command, " ")
	cmd = exec.Command(args[0], args[1:]...)

	if _, err = cmd.StdoutPipe(); err != nil {
		err = fmt.Errorf("can't get stdout pipe for command: %s", err)
		return cmd, err
	}

	if _, err = cmd.StderrPipe(); err != nil {
		err = fmt.Errorf("can't get stderr pipe for command: %s", err)
		return cmd, err
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err = cmd.Start(); err != nil {
		err = fmt.Errorf("can't start command: %s", err)
		return cmd, err
	}
	return cmd, err
}

// Run the command in the given string and restart it after
// a message was received on the buildDone channel.
func runner(command string, startRun <-chan struct{}) {
	var currentProcess *os.Process
	for {
		<-startRun

		if currentProcess != nil {
			killProcess(currentProcess)
		}

		cmd, err := startCommand(command)
		if err != nil {
			logger.Fatal("Could not start command")
		}

		currentProcess = cmd.Process
	}
}

func killProcess(process *os.Process) {
	if err := process.Kill(); err != nil {
		logger.Fatal("Could not kill child process. Aborting due to danger of infinite forks.")
	}

	if _, err := process.Wait(); err != nil {
		logger.Fatal("Could not wait for child process. Aborting due to danger of infinite forks.")
	}

	logger.Println("Reloaded")
}

func main() {
	flag.Var(&flag_excludedDirs, "exclude-dir", " Don't watch directories matching this name")
	flag.Var(&flag_excludedFiles, "exclude", " Don't watch files matching this name")
	flag.Var(&flag_includedFiles, "include", " Watch files matching this name")

	flag.Parse()
	logger = log.New(os.Stdout, "", log.LstdFlags)

	if flag.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "program is required as last parameter.\n")
		os.Exit(1)
	}
	flag_command := flag.Arg(0)

	if *flag_directory == "" {
		fmt.Fprintf(os.Stderr, "-dir=... is required.\n")
		os.Exit(1)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		logger.Fatal(err)
	}
	defer watcher.Close()

	if *flag_recursive == true {
		err = filepath.Walk(*flag_directory, func(path string, info os.FileInfo, err error) error {
			if err == nil && info.IsDir() {
				if flag_excludedDirs.Matches(info.Name()) {
					return filepath.SkipDir
				} else {
					return watcher.Watch(path)
				}
			}
			return err
		})

		if err != nil {
			logger.Fatal("filepath.Walk():", err)
		}

	} else {
		if err := watcher.Watch(*flag_directory); err != nil {
			logger.Fatal("watcher.Watch():", err)
		}
	}

	// take command name to be used as pattern as well
	cmdName := strings.Replace(flag_command, "./", "", -1)
	fullPattern := fmt.Sprintf("(%s|%s)$", cmdName, *flag_pattern)
	logger.Println("Full pattern:", fullPattern)

	pattern := regexp.MustCompile(fullPattern)
	startRun := make(chan struct{}, 20)
	startRun <- struct{}{}
	go runner(flag_command, startRun)

	for {
		select {
		case ev := <-watcher.Event:
			if ev.Name != "" {
				base := filepath.Base(ev.Name)

				if flag_includedFiles.Matches(base) || matchesPattern(pattern, ev.Name) {
					if !flag_excludedFiles.Matches(base) {
						startRun <- struct{}{}
					}
				}
			}

		case err := <-watcher.Error:
			if v, ok := err.(*os.SyscallError); ok {
				if v.Err == syscall.EINTR {
					continue
				}
				logger.Fatal("watcher.Error: SyscallError:", v)
			}
			logger.Fatal("watcher.Error:", err)
		}
	}
}

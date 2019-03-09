/*
rebuild watches your .exe, .html, .js, etc. files in a directory and
invokes go build and reload if file changes.
*/
package main

import (
	"flag"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/tadvi/beep"

	"github.com/howeyc/fsnotify"
)

// Default pattern to match files that can trigger a reload
const FilePatternNoBuilder = `.+\.htm$|.+\.html$|.+\.js$|.+\.css$|.+\.png$|.+\.jpg$|.+\.exe$|.+\.wasm$`

//var tray *systray.Systray
var currentProcess *os.Process

var config struct {
	Directory string
	Command   string
	Pattern   string
	Recursive bool
	Beep      bool
}

func main() {
	flag.StringVar(&config.Directory, "d", ".", "directory to watch for changes")
	//flag.StringVar(&config.Command, "c", "", "command to run and restart after build if blank will use .exe")
	flag.StringVar(&config.Pattern, "pattern", FilePatternNoBuilder, "pattern of watched files")
	flag.BoolVar(&config.Recursive, "recursive", true, "watch all dirs. recursively")
	flag.BoolVar(&config.Beep, "beep", true, " beep on failure")

	flag.Parse()

	if len(flag.Args()) > 0 {
		config.Command = flag.Args()[0]
	} else {
		log.Println("Pass command name to watch after as first non-flag command-line argument")
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		for range c {
			killProcess(currentProcess)
			log.Fatal("Received Ctrl-C. Exit.")
		}
	}()

	fullPattern := config.Pattern

	if config.Directory == "" {
		fatalf("-d=... with directory is required.")
	}

	cmd := config.Command
	if config.Command == "" {
		// search for executable, only works on Windows.
		match, err := filepath.Glob(config.Directory + "/*.exe")
		if err != nil {
			fatalf("%v", err)
		}
		switch m := len(match); {
		case m == 0:
			fatalf("No executable in this folder")
		case m == 1:
			cmd = match[0]
		case m > 1:
			fatalf("Too many executables in this folder")
		}
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		fatalf("%v", err)
	}
	defer watcher.Close()

	if config.Recursive == true {
		err = filepath.Walk(config.Directory, func(path string, info os.FileInfo, err error) error {
			if err == nil && info.IsDir() {
				return watcher.Watch(path)
			}
			return err
		})

		if err != nil {
			fatalf("filepath.Walk:", err)
		}

	} else {
		if err := watcher.Watch(config.Directory); err != nil {
			fatalf("watcher.Watch:", err)
		}
	}

	log.Println("Patterns:", fullPattern)
	log.Println("Recursive:", config.Recursive)
	log.Println("Running", cmd)
	log.Println()

	pattern := regexp.MustCompile(fullPattern)
	startRun := make(chan struct{}, 50)
	go runner(cmd, startRun)

	for {
		select {
		case ev := <-watcher.Event:
			if ev.Name != "" {
				if matchesPattern(pattern, ev.Name) {
					startRun <- struct{}{}
				}
			}

		case err := <-watcher.Error:
			if v, ok := err.(*os.SyscallError); ok {
				if v.Err == syscall.EINTR {
					continue
				}
				fatalf("watcher.Error: SyscallError:", v)
			}
			fatalf("watcher.Error:", err)
		}
	}
}

func matchesPattern(pattern *regexp.Regexp, file string) bool {
	return pattern.MatchString(file)
}

// Run the command in the given string and restart it after
// a message was received on the buildDone channel.
func runner(command string, startRun chan struct{}) {
	var last time.Time
	var restarted int

	for {
		args := strings.Split(command, " ")

		if currentProcess != nil {
			killProcess(currentProcess)
		}

		if restarted > 9 {
			fatalf("repeated restarts")
		}
		if time.Since(last).Seconds() > 15 {
			restarted = 0
		}

		time.Sleep(2 * time.Second)
		// We do not have other concurrent receivers so it is safe to drain
		// the channel this way.
		for len(startRun) > 0 {
			<-startRun
		}

		last = time.Now()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Start(); err != nil {
			fatalf("can't start command: %s", err)
		}

		currentProcess = cmd.Process
		log.Println("------ Reload ------")
		<-startRun
		restarted++
	}
}

func killProcess(process *os.Process) {
	if process == nil {
		return
	}

	if err := process.Kill(); err != nil {
		log.Println("Subprocess died.")
		return
	}

	if _, err := process.Wait(); err != nil {
		log.Println("Failed wait for subprocess.")
	}
}

func fatalf(format string, v ...interface{}) {
	if config.Beep {
		beep.Alert()
	}
	if len(v) > 0 {
		log.Fatalf(format, v)
		return
	}
	log.Fatal(format)
}

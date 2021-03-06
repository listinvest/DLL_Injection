package main

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"github.com/itchio/ox/syscallex"
	"github.com/itchio/ox/winox"
	"github.com/itchio/ox/winox/execas"
	"golang.org/x/sys/windows"
)

var (
	kernel32mod      = windows.NewLazySystemDLL("kernel32.dll")
	procLoadLibraryW = kernel32mod.NewProc("LoadLibraryW")

	user32mod       = windows.NewLazySystemDLL("user32.dll")
	procMessageBoxW = user32mod.NewProc("MessageBoxW")
)

func injectPID(dllFile string, pid int64) {
	var err error

	dllFile, err = filepath.Abs(dllFile)
	must(err)

	_, err = os.Stat(dllFile)
	must(err)

	log.Printf("Injecting (%s) into PID (%d)", dllFile, pid)
	doInject(dllFile, int64(pid))
	log.Printf("Done injecting")
}

func injectExe(dllFile string, exeFile string) {
	var err error

	exeFile, err = filepath.Abs(exeFile)
	must(err)
	_, err = os.Stat(exeFile)
	must(err)

	log.Printf("Injecting (%s) into (%s)", dllFile, exeFile)

	cwd, err := os.Getwd()
	must(err)

	cmd := execas.Command(exeFile)
	cmd.Dir = cwd
	cmd.Env = os.Environ()
	cmd.Stdout = os.Stdout
	cmd.Stdin = os.Stdin

	var creationFlags uint32 = 0 /* syscallex.CREATE_SUSPENDED */
	cmd.SysProcAttr = &syscallex.SysProcAttr{
		CreationFlags: creationFlags,
	}

	log.Printf("Creating process suspended...")
	err = cmd.Start()
	must(err)

	log.Printf("Process handle: %012x", cmd.SysProcAttr.ProcessHandle)
	log.Printf(" Thread handle: %012x", cmd.SysProcAttr.ThreadHandle)

	pid := cmd.ProcessState.Pid()
	go func() {
		log.Printf("Sleeping a bit before injection...")
		time.Sleep(500 * time.Millisecond)
		log.Printf("Okay, inject now!")
		doInject(dllFile, int64(pid))
	}()

	log.Printf("Okay, waiting now")
	err = cmd.Wait()
	must(err)

	log.Printf("And we're done!")
}

func doInject(dllFile string, pid int64) {
	var err error

	dllFile, err = filepath.Abs(dllFile)
	must(err)
	_, err = os.Stat(dllFile)
	must(err)

	processHandle, err := syscall.OpenProcess(syscallex.PROCESS_ALL_ACCESS, false, uint32(pid))
	must(err)

	log.Printf("Process handle: %012x", processHandle)

	// log.Printf("Suspending a thread")
	// _, err = syscallex.SuspendThread(cmd.SysProcAttr.ThreadHandle)
	// must(err)

	dllFile16 := syscall.StringToUTF16(dllFile)
	size := uintptr(len(dllFile16) * 2 /* wchars */)

	log.Printf("Contents of dllFile16: %x", dllFile16)

	log.Printf("Allocating %d bytes of memory", size)
	mem, err := syscallex.VirtualAllocEx(
		processHandle,
		0,
		size,
		syscallex.MEM_RESERVE|syscallex.MEM_COMMIT,
		syscallex.PAGE_READWRITE,
	)
	must(err)
	log.Printf("Allocated memory at %012x", mem)

	log.Printf("Writing to process memory...")
	writtenSize, err := syscallex.WriteProcessMemory(
		processHandle,
		mem,
		unsafe.Pointer(&dllFile16[0]),
		uint32(size),
	)
	must(err)
	log.Printf("Wrote %d bytes to process memory", writtenSize)

	log.Printf("LoadLibraryW address = %012x", procLoadLibraryW.Addr())
	threadHandle, threadId, err := syscallex.CreateRemoteThread(
		processHandle,
		nil,
		0,
		procLoadLibraryW.Addr(),
		mem,
		0,
	)
	must(err)

	defer winox.SafeRelease(uintptr(threadHandle))

	log.Printf("Created remote thread: ID %012x, handle %012x", threadId, threadHandle)

	beforeWait := time.Now()
	event, err := syscall.WaitForSingleObject(threadHandle, 4000)
	log.Printf("(Wait took %v)", time.Since(beforeWait))
	must(err)
	if event == syscall.WAIT_OBJECT_0 {
		log.Printf("Oh hey injection... worked?")
		exitCode, err := syscallex.GetExitCodeThread(threadHandle)
		must(err)
		log.Printf("Thread exit code: %012x", exitCode)
	} else {
		must(errors.New("Injection failed"))
	}

	log.Printf("Waiting a bit till we resume...")
	time.Sleep(500 * time.Millisecond)

	// log.Printf("Resuming!")
	// _, err = syscallex.ResumeThread(cmd.SysProcAttr.ThreadHandle)
	// must(err)
}

//go:build windows && suspendqa

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"golang.org/x/sys/windows"
	"phptools/internal/desktop"
)

// This harness only exists in explicitly tagged acceptance binaries. It drives
// the app's public window/WebView methods, never sends Windows input, and never
// starts PHP, MySQL, Nginx or Redis.
var suspendQAReportPath = flag.String("suspend-qa-report", filepath.Join("build", "qa", "suspend-acceptance.json"), "WebView 挂起验收报告")
var suspendQAStateCalls atomic.Uint64

func suspendAcceptanceStateRead() { suspendQAStateCalls.Add(1) }

type suspendQACheck struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Details string `json:"details,omitempty"`
}

type suspendQAMemory struct {
	Phase             string             `json:"phase"`
	At                string             `json:"at"`
	WorkingSetBytes   uint64             `json:"workingSetBytes"`
	PrivateBytes      uint64             `json:"privateBytes"`
	PrivateWorkingSet uint64             `json:"privateWorkingSetBytes"`
	StateCalls        uint64             `json:"getStateCalls"`
	Processes         []suspendQAProcess `json:"processes"`
}

type suspendQAProcess struct {
	PID               uint32 `json:"pid"`
	Name              string `json:"name"`
	WorkingSetBytes   uint64 `json:"workingSetBytes"`
	PrivateBytes      uint64 `json:"privateBytes"`
	PrivateWorkingSet uint64 `json:"privateWorkingSetBytes"`
}

type suspendQAReport struct {
	Started   string            `json:"started"`
	Completed string            `json:"completed"`
	PID       int               `json:"pid"`
	Profile   string            `json:"profile"`
	Passed    bool              `json:"passed"`
	Error     string            `json:"error,omitempty"`
	Checks    []suspendQACheck  `json:"checks"`
	Memory    []suspendQAMemory `json:"memory"`
	Notes     []string          `json:"notes"`
}

type suspendQAJSResult struct {
	ID    uint64          `json:"id"`
	Value json.RawMessage `json:"value"`
	Error string          `json:"error"`
}

type suspendQARunner struct {
	window   *application.WebviewWindow
	bridge   *desktop.App
	activity *windowActivityController
	notify   func()
	report   suspendQAReport
	url      string
	mu       sync.Mutex
	next     uint64
	jsWait   map[uint64]chan suspendQAJSResult
	health   atomic.Uint64
}

func startSuspendAcceptance(native *application.App, window *application.WebviewWindow, bridge *desktop.App, activity *windowActivityController, notify func()) {
	ready := make(chan struct{})
	var readyOnce sync.Once
	window.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) {
		readyOnce.Do(func() { close(ready) })
	})
	go func() {
		profile := flag.Lookup("config-dir").Value.String()
		qa := &suspendQARunner{window: window, bridge: bridge, activity: activity, notify: notify, jsWait: map[uint64]chan suspendQAJSResult{}, report: suspendQAReport{
			Started: time.Now().Format(time.RFC3339), PID: os.Getpid(), Profile: profile,
			Checks: []suspendQACheck{}, Memory: []suspendQAMemory{},
			Notes: []string{"Real WebView2; no CDP attachment or mocked application state.", "Memory includes only this isolated acceptance process and its descendants; background PHP/MySQL services are excluded.", "Private working set is resident private memory; private bytes are committed private memory. Suspension does not guarantee a fixed memory reduction."},
		}}
		var runErr error
		var safeToQuit bool
		defer func() {
			qa.report.Completed = time.Now().Format(time.RFC3339)
			qa.report.Passed = runErr == nil
			if runErr != nil {
				qa.report.Error = runErr.Error()
				log.Printf("SUSPEND QA FAILED: %v", runErr)
			}
			if err := qa.writeReport(); err != nil {
				log.Printf("SUSPEND QA report: %v", err)
			}
			// Quit only this uniquely identified acceptance instance. The main
			// bridge's Exit hook grants the ordinary application shutdown.
			if safeToQuit {
				if err := bridge.Quit(); err != nil {
					log.Printf("SUSPEND QA shutdown: %v", err)
				}
			}
		}()
		if instanceID == "local.phptools.desktop" || profile == "" || !filepath.IsAbs(profile) {
			runErr = errors.New("acceptance requires a unique instanceID and an absolute, separate --config-dir")
			return
		}
		initial, err := bridge.GetState()
		if err != nil {
			runErr = err
			return
		}
		if initial.Settings.Root != "" || len(initial.Instances) != 0 {
			runErr = errors.New("acceptance requires an uninitialized profile without service instances")
			return
		}
		safeToQuit = true
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			runErr = err
			return
		}
		qa.url = "http://" + listener.Addr().String()
		server := &http.Server{Handler: http.HandlerFunc(qa.serveHTTP), ReadHeaderTimeout: 3 * time.Second}
		go func() { _ = server.Serve(listener) }()
		defer server.Close()
		stopResults := native.Event.On("suspendqa:result", func(event *application.CustomEvent) {
			raw, err := json.Marshal(event.Data)
			if err != nil {
				return
			}
			var result suspendQAJSResult
			if json.Unmarshal(raw, &result) == nil {
				qa.deliverJSResult(result)
			}
		})
		defer stopResults()
		select {
		case <-ready:
		case <-time.After(45 * time.Second):
			runErr = errors.New("WebView runtime was not ready within 45 seconds")
			return
		}
		runErr = qa.run(profile)
	}()
}

func (q *suspendQARunner) run(profile string) error {
	// Give Vue its normal startup rather than replacing its page with a fixture.
	time.Sleep(time.Second)
	value, err := q.js(`
const deadline=Date.now()+10000;
while(!document.querySelector("input")){if(Date.now()>deadline)throw Error("setup form did not mount");await new Promise(resolve=>setTimeout(resolve,100));}
const input=document.querySelector("input");
const marker="D:\\Suspend Acceptance\\unsaved-input";
Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,"value").set.call(input,marker);
input.dispatchEvent(new Event("input",{bubbles:true}));
window.__suspendAcceptance={started:performance.timeOrigin,marker,ticks:0};
window.__suspendAcceptance.timer=setInterval(()=>window.__suspendAcceptance.ticks++,100);
return {input:input.value,heading:document.querySelector("h1")?.textContent,origin:performance.timeOrigin};`)
	if err != nil {
		return fmt.Errorf("real frontend ready: %w", err)
	}
	q.record("Real frontend and unsaved input initialized", string(value))
	if err = q.checkHealth(); err != nil {
		return err
	}
	// Warm the real layout before collecting either memory condition.
	time.Sleep(5 * time.Second)
	before := suspendQAStateCalls.Load()
	time.Sleep(4 * time.Second)
	after := suspendQAStateCalls.Load()
	if after-before < 2 {
		return fmt.Errorf("visible frontend did not poll GetState: %d -> %d (StateRead hook required)", before, after)
	}
	q.record("Visible frontend polls GetState", fmt.Sprintf("%d calls in four seconds", after-before))
	if err = q.memory("visible-before"); err != nil {
		return err
	}

	// A plain native hide is the comparison condition: the frontend observes
	// document.visibilityState, but WebView2 remains unsuspended.
	q.window.Hide()
	if err = q.window.ResumeWebView(); err != nil {
		return fmt.Errorf("ordinary-hide baseline resume: %w", err)
	}
	time.Sleep(2 * time.Second)
	if err = q.requireSuspended(false); err != nil {
		return fmt.Errorf("ordinary-hide baseline: %w", err)
	}
	before = suspendQAStateCalls.Load()
	time.Sleep(5 * time.Second)
	if suspendQAStateCalls.Load() != before {
		return fmt.Errorf("GetState continued while document hidden: %d -> %d", before, suspendQAStateCalls.Load())
	}
	q.record("Hidden frontend stops polling without native suspension", fmt.Sprintf("GetState remained at %d for five seconds", before))
	if err = q.checkHealth(); err != nil {
		return err
	}
	if err = q.memory("hidden-without-suspension"); err != nil {
		return err
	}

	// Drive exactly the production controller used by the tray close hook.
	q.activity.Hide()
	if err = q.waitSuspended(true, 10*time.Second); err != nil {
		return fmt.Errorf("production tray hide: %w", err)
	}
	q.record("Tray hide suspends real WebView2", "IsWebViewSuspended=true")
	before = suspendQAStateCalls.Load()
	for i := 0; i < 100; i++ {
		q.notify()
	}
	time.Sleep(10 * time.Second)
	if err = q.requireSuspended(true); err != nil {
		return fmt.Errorf("hidden background state events woke WebView2: %w", err)
	}
	if suspendQAStateCalls.Load() != before {
		return fmt.Errorf("hidden state events caused GetState calls: %d -> %d", before, suspendQAStateCalls.Load())
	}
	q.record("Hidden task/state notifications do not wake WebView2", "100 production notifications; no GetState calls; still suspended after ten seconds")
	if err = q.checkHealth(); err != nil {
		return err
	}
	if err = q.memory("hidden-with-suspension"); err != nil {
		return err
	}

	q.activity.Show()
	if err = q.waitSuspended(false, 5*time.Second); err != nil {
		return fmt.Errorf("tray restore: %w", err)
	}
	if err = q.waitStateAfter(before, 3*time.Second); err != nil {
		return err
	}
	value, err = q.js(`
const saved=window.__suspendAcceptance;
if(!saved)throw Error("page state was lost");
if(saved.started!==performance.timeOrigin)throw Error("page was reloaded");
if(document.querySelector("input")?.value!==saved.marker)throw Error("unsaved form value was lost");
if(document.hidden)throw Error("restored document is still hidden");
return {input:document.querySelector("input").value,ticks:saved.ticks,origin:performance.timeOrigin};`)
	if err != nil {
		return fmt.Errorf("restored page state: %w", err)
	}
	q.record("Tray restore resumes polling and retains unsaved input", string(value))
	before = suspendQAStateCalls.Load()
	q.notify()
	if err = q.waitStateAfter(before, 2*time.Second); err != nil {
		return fmt.Errorf("restored state-change event: %w", err)
	}
	q.record("Visible state notifications refresh again", "GetState responds to the production notification hook")

	// Immediate opposite operations exercise a pending asynchronous suspend
	// callback. Final visibility must win even if the callback completes later.
	for i := 0; i < 12; i++ {
		q.activity.Hide()
		q.activity.Show()
	}
	time.Sleep(time.Second)
	if err = q.requireSuspended(false); err != nil {
		return fmt.Errorf("rapid hide/show left page suspended: %w", err)
	}
	if !q.activity.IsActive() || !q.window.IsVisible() {
		return errors.New("rapid hide/show left window inactive or hidden")
	}
	value, err = q.js(`if(document.hidden)throw Error("rapid restore remained hidden");if(document.querySelector("input")?.value!==window.__suspendAcceptance?.marker)throw Error("rapid hide/show lost form value");return {visible:!document.hidden,input:document.querySelector("input").value};`)
	if err != nil {
		return err
	}
	q.record("Rapid hide/show handles pending suspend callbacks", "12 immediate cycles; visible, active and unsuspended; "+string(value))
	if err = q.memory("visible-after-restore"); err != nil {
		return err
	}
	q.record("Backend loopback service stays available", fmt.Sprintf("%d successful independent HTTP requests", q.health.Load()))
	_, err = q.js(`clearInterval(window.__suspendAcceptance.timer);return true;`)
	return err
}

func (q *suspendQARunner) checkHealth() error {
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(q.url + "/health")
	if err != nil {
		return fmt.Errorf("background loopback service: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 100))
	if err != nil || response.StatusCode != http.StatusOK || string(raw) != "background service alive" {
		return fmt.Errorf("background loopback service: status %d body %q error %v", response.StatusCode, raw, err)
	}
	return nil
}

func (q *suspendQARunner) requireSuspended(wanted bool) error {
	actual, err := q.window.IsWebViewSuspended()
	if err != nil {
		return err
	}
	if actual != wanted {
		return fmt.Errorf("IsWebViewSuspended=%t, want %t", actual, wanted)
	}
	return nil
}

func (q *suspendQARunner) waitSuspended(wanted bool, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		err := q.requireSuspended(wanted)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (q *suspendQARunner) waitStateAfter(before uint64, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for suspendQAStateCalls.Load() <= before {
		if time.Now().After(deadline) {
			return fmt.Errorf("GetState did not resume within %s (count %d)", timeout, before)
		}
		time.Sleep(25 * time.Millisecond)
	}
	return nil
}

// PROCESS_MEMORY_COUNTERS_EX2 is supported on this application's Windows 10+
// target and reports the private resident working set without double-counting
// pages shared by the WebView2 browser/renderer/GPU processes.
type suspendQAMemoryCounters struct {
	Size                       uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
	PrivateUsage               uintptr
	PrivateWorkingSetSize      uintptr
	SharedCommitUsage          uintptr
}

var suspendQAGetProcessMemoryInfo = windows.NewLazySystemDLL("kernel32.dll").NewProc("K32GetProcessMemoryInfo")

func (q *suspendQARunner) memory(phase string) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	all := []windows.ProcessEntry32{}
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		all = append(all, entry)
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return err
	}
	owned := map[uint32]bool{uint32(os.Getpid()): true}
	for changed := true; changed; {
		changed = false
		for _, process := range all {
			if owned[process.ParentProcessID] && !owned[process.ProcessID] {
				owned[process.ProcessID] = true
				changed = true
			}
		}
	}
	sample := suspendQAMemory{Phase: phase, At: time.Now().Format(time.RFC3339Nano), StateCalls: suspendQAStateCalls.Load(), Processes: []suspendQAProcess{}}
	for _, process := range all {
		if !owned[process.ProcessID] {
			continue
		}
		handle, openErr := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, process.ProcessID)
		if errors.Is(openErr, windows.ERROR_INVALID_PARAMETER) {
			continue // A transient child exited since the process snapshot.
		}
		if openErr != nil {
			return fmt.Errorf("memory query process %d: %w", process.ProcessID, openErr)
		}
		counters := suspendQAMemoryCounters{Size: uint32(unsafe.Sizeof(suspendQAMemoryCounters{}))}
		ok, _, callErr := suspendQAGetProcessMemoryInfo.Call(uintptr(handle), uintptr(unsafe.Pointer(&counters)), uintptr(counters.Size))
		_ = windows.CloseHandle(handle)
		if ok == 0 {
			return fmt.Errorf("K32GetProcessMemoryInfo process %d: %w", process.ProcessID, callErr)
		}
		p := suspendQAProcess{PID: process.ProcessID, Name: windows.UTF16ToString(process.ExeFile[:]), WorkingSetBytes: uint64(counters.WorkingSetSize), PrivateBytes: uint64(counters.PrivateUsage), PrivateWorkingSet: uint64(counters.PrivateWorkingSetSize)}
		sample.Processes = append(sample.Processes, p)
		sample.WorkingSetBytes += p.WorkingSetBytes
		sample.PrivateBytes += p.PrivateBytes
		sample.PrivateWorkingSet += p.PrivateWorkingSet
	}
	if len(sample.Processes) < 2 || sample.PrivateWorkingSet == 0 {
		return fmt.Errorf("memory sample lacks WebView2 descendants or private working-set counters: %+v", sample)
	}
	q.report.Memory = append(q.report.Memory, sample)
	log.Printf("SUSPEND QA MEMORY %s: private working set %.1f MiB, private bytes %.1f MiB, %d processes", phase, float64(sample.PrivateWorkingSet)/(1024*1024), float64(sample.PrivateBytes)/(1024*1024), len(sample.Processes))
	return nil
}

func (q *suspendQARunner) record(name, details string) {
	q.report.Checks = append(q.report.Checks, suspendQACheck{Name: name, Passed: true, Details: details})
	log.Printf("SUSPEND QA PASS %s: %s", name, details)
}

func (q *suspendQARunner) writeReport() error {
	raw, err := json.MarshalIndent(q.report, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(*suspendQAReportPath), 0755); err != nil {
		return err
	}
	return os.WriteFile(*suspendQAReportPath, append(raw, '\n'), 0600)
}

func (q *suspendQARunner) serveHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.URL.Path == "/health" {
		q.health.Add(1)
		_, _ = io.WriteString(w, "background service alive")
		return
	}
	http.NotFound(w, r)
}

func (q *suspendQARunner) deliverJSResult(result suspendQAJSResult) {
	q.mu.Lock()
	channel := q.jsWait[result.ID]
	q.mu.Unlock()
	if channel != nil {
		select {
		case channel <- result:
		default:
		}
	}
}

func (q *suspendQARunner) js(body string) (json.RawMessage, error) {
	q.mu.Lock()
	q.next++
	id := q.next
	channel := make(chan suspendQAJSResult, 1)
	q.jsWait[id] = channel
	q.mu.Unlock()
	defer func() { q.mu.Lock(); delete(q.jsWait, id); q.mu.Unlock() }()
	// The real page's CSP deliberately limits connect-src to itself. Send the
	// existing runtime's Events.Emit request on that same origin. Importing a
	// second runtime module would replace the live app's event dispatcher.
	q.window.ExecJS(fmt.Sprintf(`(async()=>{let result={id:%d};try{result.value=await(async()=>{%s})();}catch(error){result.error=String(error&&error.stack||error);}await fetch(location.origin+"/wails/runtime",{method:"POST",headers:{"Content-Type":"application/json","x-wails-client-id":window._wails.clientId},body:JSON.stringify({object:3,method:0,args:{name:"suspendqa:result",data:result}})});})()`, id, body))
	select {
	case result := <-channel:
		if result.Error != "" {
			return nil, errors.New(result.Error)
		}
		return result.Value, nil
	case <-time.After(15 * time.Second):
		return nil, errors.New("WebView JavaScript assertion did not report within 15 seconds")
	}
}

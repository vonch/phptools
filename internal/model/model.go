package model

import "time"

type Package struct {
	ID                 string `json:"id"`
	Kind               string `json:"kind"`
	Version            string `json:"version"`
	Arch               string `json:"arch"`
	URL                string `json:"url"`
	SHA256             string `json:"sha256"`
	SignatureURL       string `json:"signatureUrl"`
	SigningKeyURL      string `json:"signingKeyUrl"`
	SigningFingerprint string `json:"signingFingerprint"`
	Source             string `json:"source"`
	SourceURL          string `json:"sourceUrl"`
	Channel            string `json:"channel"`
	EOL                bool   `json:"eol"`
	Verified           bool   `json:"verified"`
	Path               string `json:"path"`
	InstalledAt        string `json:"installedAt"`
}

type Instance struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	PackageID      string `json:"packageId"`
	Port           int    `json:"port"`
	ConfigPath     string `json:"configPath"`
	DataDir        string `json:"dataDir"`
	LogPath        string `json:"logPath"`
	PID            int    `json:"pid"`
	ProcessCreated uint64 `json:"processCreated"`
	Executable     string `json:"executable"`
	State          string `json:"state"`
	Error          string `json:"error"`
	CreatedAt      string `json:"createdAt"`
}

type InstanceInput struct {
	Name      string `json:"name"`
	PackageID string `json:"packageId"`
	Port      int    `json:"port"`
	Password  string `json:"password"`
}

// ManagedDatabase is a database remembered by PHPTools. The password is not
// stored in this record; MySQL receives it only when the record is synced.
type ManagedDatabase struct {
	ID         string `json:"id"`
	InstanceID string `json:"instanceId"`
	Name       string `json:"name"`
	User       string `json:"user"`
	Host       string `json:"host"`
	Password   string `json:"password,omitempty"`
	State      string `json:"state"`
	Error      string `json:"error"`
	CreatedAt  string `json:"createdAt"`
}

type DatabaseInfo struct {
	Name      string `json:"name"`
	Charset   string `json:"charset"`
	Collation string `json:"collation"`
	Tables    int64  `json:"tables"`
	SizeBytes int64  `json:"sizeBytes"`
	System    bool   `json:"system"`
}

type DatabaseTool struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Path    string `json:"path"`
	URL     string `json:"url"`
	Source  string `json:"source"`
	Custom  bool   `json:"custom"`
	CanOpen bool   `json:"canOpen"`
	Note    string `json:"note"`
}

type DatabaseAccount struct {
	User      string `json:"user"`
	Host      string `json:"host"`
	Protected bool   `json:"protected"`
}

type DatabaseAccountInput struct {
	User     string `json:"user"`
	Host     string `json:"host"`
	Password string `json:"password"`
}

type DatabasePrivilegeInput struct {
	User       string   `json:"user"`
	Host       string   `json:"host"`
	Database   string   `json:"database"`
	Privileges []string `json:"privileges"`
}

type Site struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Domain        string `json:"domain"`
	SecondDomain  string `json:"secondDomain,omitempty"`
	Root          string `json:"root"`
	Entry         string `json:"entry"`
	Port          int    `json:"port"`
	HTTPS         bool   `json:"https"`
	ForceHTTPS    bool   `json:"forceHttps"`
	PHPInstanceID string `json:"phpInstanceId"`
	PHPPathInfo   bool   `json:"phpPathInfo"`
	Enabled       bool   `json:"enabled"`
	Rewrite       string `json:"rewrite"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
	LastActiveAt  string `json:"lastActiveAt"`
	ProjectFolder string `json:"projectFolder,omitempty"`
}

type SiteEntrySelection struct {
	Entry    string `json:"entry"`
	Selected bool   `json:"selected"`
}

type RewritePreset struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	Rules          string `json:"rules"`
	SuggestedEntry string `json:"suggestedEntry"`
	SourceURL      string `json:"sourceUrl"`
}

type Task struct {
	ID         string  `json:"id"`
	Action     string  `json:"action"`
	TargetID   string  `json:"targetId"`
	State      string  `json:"state"`
	Progress   float64 `json:"progress"`
	BytesTotal int64   `json:"bytesTotal"`
	BytesDone  int64   `json:"bytesDone"`
	Message    string  `json:"message"`
	Error      string  `json:"error"`
	StartedAt  string  `json:"startedAt"`
}

type Settings struct {
	Root              string `json:"root"`
	SuggestedRoot     string `json:"suggestedRoot"`
	Version           string `json:"version"`
	UpdateManifestURL string `json:"-"`
}

type AutoStartStatus struct {
	Enabled  bool   `json:"enabled"`
	Conflict string `json:"conflict"`
}

type SidebarItem struct {
	Key    string `json:"key"`
	Hidden bool   `json:"hidden"`
	Label  string `json:"label"`
}

type SiteViewPreferences struct {
	HiddenColumns []string `json:"hiddenColumns"`
	SiteOrder     []string `json:"siteOrder"`
	SortBy        string   `json:"sortBy"`
	SortDirection string   `json:"sortDirection"`
}

type Diagnostic struct {
	Level   string `json:"level"`
	Message string `json:"message"`
}

type RuntimeDependency struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	State      string   `json:"state"`
	Version    string   `json:"version"`
	Message    string   `json:"message"`
	RequiredBy []string `json:"requiredBy"`
	Guide      string   `json:"guide"`
}

type DependencyReport struct {
	CheckedAt string              `json:"checkedAt"`
	Items     []RuntimeDependency `json:"items"`
	Warnings  []string            `json:"warnings"`
}

type HealthItem struct {
	ID         string `json:"id"`
	TargetID   string `json:"targetId"`
	TargetName string `json:"targetName"`
	Check      string `json:"check"`
	Status     string `json:"status"`
	Message    string `json:"message"`
	Suggestion string `json:"suggestion"`
	Repairable bool   `json:"repairable"`
	RepairID   string `json:"repairId"`
}

type HealthReport struct {
	StartedAt   string       `json:"startedAt"`
	CompletedAt string       `json:"completedAt"`
	Trigger     string       `json:"trigger"`
	Full        bool         `json:"full"`
	Items       []HealthItem `json:"items"`
}

type UpdateInfo struct {
	CurrentVersion string `json:"currentVersion"`
	LatestVersion  string `json:"latestVersion"`
	Available      bool   `json:"available"`
	Mandatory      bool   `json:"mandatory"`
	ReleaseDate    string `json:"releaseDate"`
	Notes          string `json:"notes"`
	DownloadURL    string `json:"-"`
	SHA256         string `json:"sha256"`
	Size           int64  `json:"size"`
	CheckedAt      string `json:"checkedAt"`
	Configured     bool   `json:"configured"`
	Error          string `json:"error"`
}

type MemoryUsage struct {
	WorkingSetBytes uint64 `json:"workingSetBytes"`
	ManagerBytes    uint64 `json:"managerBytes"`
	WebViewBytes    uint64 `json:"webViewBytes"`
	PrivateBytes    uint64 `json:"privateBytes"`
	ProcessCount    int    `json:"processCount"`
	SampledAt       string `json:"sampledAt"`
	Partial         bool   `json:"partial"`
	Error           string `json:"error"`
}

type SiteCertificate struct {
	SiteID   string   `json:"siteId"`
	SiteName string   `json:"siteName"`
	Domains  []string `json:"domains"`
	Kind     string   `json:"kind"`
	Issuer   string   `json:"issuer"`
	NotAfter string   `json:"notAfter"`
	Eligible bool     `json:"eligible"`
	Reason   string   `json:"reason"`
}

type CertificatePage struct {
	Email string            `json:"email"`
	Sites []SiteCertificate `json:"sites"`
}

type Snapshot struct {
	Settings     Settings            `json:"settings"`
	Sidebar      []SidebarItem       `json:"sidebar"`
	SiteView     SiteViewPreferences `json:"siteView"`
	Packages     []Package           `json:"packages"`
	Instances    []Instance          `json:"instances"`
	Sites        []Site              `json:"sites"`
	Tasks        []Task              `json:"tasks"`
	Diagnostics  []Diagnostic        `json:"diagnostics"`
	HealthReport *HealthReport       `json:"healthReport"`
	MemoryUsage  *MemoryUsage        `json:"memoryUsage"`
	UpdateInfo   *UpdateInfo         `json:"updateInfo"`
}

func Now() string { return time.Now().UTC().Format(time.RFC3339) }

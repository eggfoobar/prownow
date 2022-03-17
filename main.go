package main

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"math/big"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/Masterminds/sprig"

	_ "embed"

	"encoding/xml"

	"github.com/GoogleCloudPlatform/testgrid/pb/test_status"

	ui "github.com/gizak/termui/v3"
	"github.com/gizak/termui/v3/widgets"
)

const (
	testGridJob              = "https://testgrid.k8s.io/%s/table?tab=%s"
	testGridDashboardSummary = "https://testgrid.k8s.io/%s/summary"
	testGridDashboardList    = "https://testgrid.k8s.io/q/list"
	prowURL                  = "https://prow.ci.openshift.org/view/gs/origin-ci-test/logs/%s"
	searchCIurl              = "https://search.ci.openshift.org/%s?search=%s&maxAge=48h&context=1&type=bug%%2Bjunit&name=&excludeName=&maxMatches=5&maxBytes=20971520&groupBy=job"
)

//go:embed template_merged.md
var templateMergedMD string

//go:embed template_merged.html
var templateMergedHTML string

//go:embed template_merged.csv
var templateMergedCSV string

var (
	rehearseJobs           string
	rehearseJobToCompare   []string
	dashboardJobs          string
	dashboardJobsToCompare map[string][]string
	depth                  int
	outputFileName         string
	serve                  string
	dahboardFilter         string
	dashboardFilterReg     *regexp.Regexp
	jobFilter              string
	jobFilterReg           *regexp.Regexp
	format                 string
	templateMerged         string = templateMergedHTML
)

func init() {
	now := time.Now().Format(time.Kitchen)
	flag.StringVar(&rehearseJobs, "rehearse", "", "list of rehearse jobs to compare, must match file names")
	flag.StringVar(&dashboardJobs, "dashboard-jobs", "", "dashboard jobs to fetch e.g. dash=job,job:dash=job")
	flag.StringVar(&dahboardFilter, "dashboard-filter", "redhat-openshift-ocp-release", "regex to filter dashboards")
	flag.StringVar(&jobFilter, "job-filter", ".*", "regex to filter dashboards")
	flag.StringVar(&outputFileName, "file", fmt.Sprintf("dataresult-%s", now), "output file")
	flag.StringVar(&format, "format", "html", "output format")
	flag.StringVar(&serve, "serve", "", "serve static files from given directory")
	flag.IntVar(&depth, "depth", 5, "display how many job statuses")
	flag.Parse()
	dashboardFilterReg = regexp.MustCompile(dahboardFilter)
	jobFilterReg = regexp.MustCompile(jobFilter)
	if rehearseJobs != "" {
		result := strings.Split(rehearseJobs, ",")
		rehearseJobToCompare = append(rehearseJobToCompare, result...)
	}
	if dashboardJobs != "" {
		dashboardJobsToCompare = make(map[string][]string)
		dashboards := strings.Split(dashboardJobs, ":")
		for _, d := range dashboards {
			if data := strings.Split(d, "="); len(data) != 2 {
				fmt.Printf("%s is not of for mat dashboard=job", d)
				os.Exit(1)
			} else {
				dashboardJobsToCompare[data[0]] = strings.Split(data[1], ",")
			}
		}
	}

	fileFormat := ".html"
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "md":
		templateMerged = templateMergedMD
		fileFormat = ".md"
	case "csv":
		templateMerged = templateMergedCSV
		fileFormat = ".csv"
	}

	if fileFormat != path.Ext(outputFileName) {
		outputFileName += fileFormat
	}

}

type JobResult struct {
	Depth       int      `json:"-"`
	Tests       []Test   `json:"tests"`
	Query       string   `json:"query"`
	ChangeLists []string `json:"changeLists"`
}

type Test struct {
	Name       string   `json:"name"`
	Statuses   []Status `json:"statuses"`
	ShortTexts []string `json:"short_texts"`
}

type Status struct {
	Count int32  `json:"count"`
	Value int32  `json:"value"`
	Name  string `json:"-"`
}

type TestGridDashboards map[string][]string

type Testsuite struct {
	XMLName  xml.Name `xml:"testsuite"`
	Text     string   `xml:",chardata"`
	Name     string   `xml:"name,attr"`
	Tests    string   `xml:"tests,attr"`
	Skipped  string   `xml:"skipped,attr"`
	Failures string   `xml:"failures,attr"`
	Time     string   `xml:"time,attr"`
	Property struct {
		Text  string `xml:",chardata"`
		Name  string `xml:"name,attr"`
		Value string `xml:"value,attr"`
	} `xml:"property"`
	Testcase []struct {
		Text    string `xml:",chardata"`
		Name    string `xml:"name,attr"`
		Time    string `xml:"time,attr"`
		Failure struct {
			Text    string `xml:",chardata"`
			Message string `xml:"message,attr"`
		} `xml:"failure"`
		SystemOut string `xml:"system-out"`
		Skipped   struct {
			Text    string `xml:",chardata"`
			Message string `xml:"message,attr"`
		} `xml:"skipped"`
	} `xml:"testcase"`
}

// START Template helper functions
func add(i, x int) int {
	return i + x
}

func name(i int32) string {
	return test_status.TestStatus_name[i]
}

func prettyList(i []string) string {
	return strings.Join(i, ", ")
}

func colorFromString(i string) string {
	c := strings.Split(i, "/")
	if len(c) == 0 {
		return "primary"
	}
	colors := []string{
		"primary",
		"secondary",
		"success",
		"danger",
		"warning",
		"info",
		"light",
		"dark",
	}
	colorIndex := fauxRandom(c[0], len(colors))
	return colors[colorIndex]
}

func emojiFromString(i string) string {
	c := strings.Split(i, "/")
	if len(c) == 0 {
		return ":grimacing:"
	}
	emojis := []string{
		":grimacing:",
		":kiss:",
		":eyes:",
		":v:",
		":dizzy_face:",
		":whale:",
		":mailbox_with_no_mail:",
		":basketball:",
	}
	emojiIndex := fauxRandom(c[0], len(emojis))
	return emojis[emojiIndex]
}

func fauxRandom(i string, max int) int {
	sha := sha1.New()
	sha.Write([]byte(i + "some_consistant_random_salt_string"))
	hashSumByte := sha.Sum(nil)
	hexVal := hex.EncodeToString(hashSumByte)
	index := 0
	if intBase16, success := new(big.Int).SetString(hexVal, 16); success {
		source := rand.NewSource(intBase16.Int64())
		random := rand.New(source)
		index = random.Intn(max)
	}
	return index
}
func toString(i interface{}) string {
	var result string
	switch i := i.(type) {
	case []string:
		result = prettyList(i)
	case string:
		result = i
	}
	return result
}
func rawURL(i interface{}) string {
	result := toString(i)
	if strings.HasPrefix(result, "rehearse") {
		return fmt.Sprintf("`%s`", result)
	}
	return fmt.Sprintf(prowURL, result)
}

func styledURL(link string, i interface{}) string {
	name := toString(i)
	result := rawURL(name)
	if strings.HasPrefix(result, "rehearse") {
		return fmt.Sprintf("`%s`", result)
	} else {
		if strings.ToLower(strings.TrimSpace(link)) == "html" {
			return fmt.Sprintf(`<a href="%s" target="_blank">%s</a>`, name, result)
		}
		return fmt.Sprintf("[%s](%s)", name, result)
	}
}

func escape(query string) string {
	query = strings.ReplaceAll(query, "[", "\\[")
	query = strings.ReplaceAll(query, "]", "\\]")
	return url.QueryEscape(query)
}

func searchCI(searchType, query string) string {
	switch strings.ToLower(strings.TrimSpace(searchType)) {
	case "chart":
		return fmt.Sprintf(searchCIurl, "chart", escape(query))
	default:
		return fmt.Sprintf(searchCIurl, "", escape(query))
	}
}

func html(str string) template.HTML {
	return template.HTML(str)
}

// END of Template helper functions

func main() {

	// Useful if you wish to serve up some of the content generated
	if len(serve) > 0 {
		fs := http.FileServer(http.Dir(serve))
		http.Handle("/", fs)

		log.Println("Listening on :9090...")
		err := http.ListenAndServe(":9090", nil)
		if err != nil {
			log.Fatal(err)
		}
		return
	}

	// Helper to print out what the `-dashboard-jobs` flag would need to be for the requested
	// dashboard jobs, use the flag when you wish to repeat the same query easily
	selectedJobs := dashboardJobsToCompare
	if len(selectedJobs) == 0 {
		selectedJobs = fetchDashboardList()
		if len(selectedJobs) > 0 {
			jobs := []string{}
			for d, j := range selectedJobs {
				jobs = append(jobs, fmt.Sprintf("%s=%s", d, strings.Join(j, ",")))
			}
			fmt.Printf("=== Flag arg for future use ===\n\n")
			fmt.Printf(`-dashboard-jobs="%s"`, strings.Join(jobs, ":"))
			fmt.Printf("\n\n=============================\n")
		}
	}

	results := map[string]JobResult{}

	// If any rehearese files are presented
	for _, job := range rehearseJobToCompare {
		m := fmt.Sprintf("ingesting: (%s)", job)
		fmt.Println(m)
		xmlData, err := ioutil.ReadFile(job)
		if err != nil {
			panic(err)
		}

		var junitTests Testsuite

		err = xml.Unmarshal(xmlData, &junitTests)
		if err != nil {
			panic(err)
		}
		results[job] = JobResult{
			Depth: depth,
		}
		for _, t := range junitTests.Testcase {
			// Test grid uses the enum value 12 to denote a failure state
			// We translate this junit
			jobs := results[job]
			status := Status{Count: 1, Value: 0}
			if t.Failure.Text != "" {
				status.Value = 12
			}
			jobs.Tests = append(jobs.Tests, Test{Name: t.Name, Statuses: []Status{status}})
			results[job] = jobs
		}
		fmt.Println("| tests :", len(results[job].Tests))
		fmt.Println("| depth :", results[job].Depth)
		fmt.Printf("|%s\n", strings.Repeat("_", len(m)-1))
	}

	// Fetch the job test results
	for dash, jobs := range selectedJobs {
		fmt.Printf("\nfetching info\n")
		fmt.Printf("| dashboard: (\033[34m%s\033[0m)\n|---\n", dash)
		for _, job := range jobs {
			var result JobResult
			m := fmt.Sprintf("| job: (\033[35m%s\033[0m)", job)
			fmt.Println(m)
			url := fmt.Sprintf(testGridJob, dash, job)
			resp, err := http.Get(url)
			if err != nil {
				fmt.Printf("Error fetching (%s)\nErr: %s", url, err)
				continue
			}
			err = json.NewDecoder(resp.Body).Decode(&result)
			if err != nil {
				fmt.Printf("Error fetching (%s)\nErr: %s", url, err)
				resp.Body.Close()
				continue
			}
			resp.Body.Close()
			result.Depth = depth
			results[job] = result
			fmt.Println("| tests :", len(results[job].Tests))
			fmt.Println("| depth :", results[job].Depth)
			fmt.Printf("|%s\n", strings.Repeat("_", len(m)-10))
		}
	}

	merged := map[string][]string{}
	for job, tests := range results {
		for _, v := range tests.Tests {
			var pos int32 = 0
			for _, sv := range v.Statuses {
				pos = (pos + sv.Count)
				if int(pos) > depth {
					break
				}
				switch sv.Value {
				case test_status.TestStatus_value["FAIL"]:
					var val string = job
					if tests.ChangeLists != nil && pos < int32(len(tests.ChangeLists)) {
						val = job + "/" + tests.ChangeLists[pos-1]
					}
					merged[v.Name] = append(merged[v.Name], val)
				}
			}
		}
	}

	file, err := os.OpenFile(outputFileName, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, os.ModePerm)
	if err != nil {
		panic(err)
	}
	defer file.Close()
	err = template.
		Must(
			template.
				New("").
				Funcs(template.FuncMap{
					"add":             add,
					"name":            name,
					"prettyList":      prettyList,
					"styledURL":       styledURL,
					"rawURL":          rawURL,
					"html":            html,
					"colorFromString": colorFromString,
					"emojiFromString": emojiFromString,
					"searchCI":        searchCI,
				}).
				Funcs(sprig.FuncMap()).
				Parse(templateMerged)).
		Execute(file, merged)
	if err != nil {
		panic(err)
	}
}

// Fetch dashboards
func fetchDashboardList() map[string][]string {
	// Fetch dashboard list
	testGridDashboards := TestGridDashboards{}

	resp, err := http.Get(testGridDashboardList)
	if err != nil {
		panic(err)
	}
	err = json.NewDecoder(resp.Body).Decode(&testGridDashboards)
	if err != nil {
		panic(err)
	}
	resp.Body.Close()

	// Filter only redhat dashboards
	keys := []string{}
	for k := range testGridDashboards {
		if !dashboardFilterReg.MatchString(k) {
			delete(testGridDashboards, k)
		} else {
			keys = append(keys, k)
		}
	}

	// Present list of dashboards to select
	selectedJobs := map[string][]string{}
	selectedDashboards, quit := selectThings("Dashboards", keys)
	if quit {
		fmt.Println("goodbye!")
		os.Exit(0)
	}

	// Present list of jobs from the dashboards to select
	for _, selectedDash := range selectedDashboards {
		keys := []string{}
		for _, k := range testGridDashboards[selectedDash] {
			if jobFilterReg.MatchString(k) {
				keys = append(keys, k)
			}
		}
		selectedJob, quit := selectThings(fmt.Sprintf("Jobs in %s", selectedDash), keys)
		if quit {
			fmt.Println("goodbye!")
			os.Exit(0)
		}
		selectedJobs[selectedDash] = append(selectedJobs[selectedDash], selectedJob...)
	}
	return selectedJobs
}

// terminal ui
func selectThings(title string, things []string) ([]string, bool) {
	if len(things) == 0 {
		fmt.Printf("List for (%s) is empty\n", title)
		return nil, false
	}
	if err := ui.Init(); err != nil {
		log.Fatalf("failed to initialize termui: %v", err)
	}
	defer ui.Close()
	selectedThings := []string{}

	l := widgets.NewList()
	l.Title = title
	l.Rows = things
	l.TextStyle = ui.NewStyle(ui.ColorMagenta)
	l.WrapText = false
	l.SetRect(0, 0, 100, 20)
	ui.Render(l)
	uiEvents := ui.PollEvents()
done:
	for {
		e := <-uiEvents
		switch e.ID {
		case "q", "<C-c>":
			return selectedThings, true
		case "j", "<Down>":
			if l.SelectedRow == len(l.Rows)-1 {
				l.ScrollTop()
			} else {
				l.ScrollDown()
			}
		case "k", "<Up>":
			if l.SelectedRow == 0 {
				l.ScrollBottom()
			} else {
				l.ScrollUp()
			}
		case "<Tab>", "<Space>":
			current := l.Rows[l.SelectedRow]
			if strings.HasPrefix(current, "[✔](fg:green):") {
				current = strings.TrimPrefix(current, "[✔](fg:green):")
				for i, item := range selectedThings {
					if item == current {
						selectedThings = append(selectedThings[:i], selectedThings[i+1:]...)
					}
				}
			} else {
				selectedThings = append(selectedThings, current)
				current = fmt.Sprintf("[✔](fg:green):%s", current)
			}
			l.Rows[l.SelectedRow] = current
		case "<Enter>":
			if len(selectedThings) == 0 {
				selectedThings = append(selectedThings, l.Rows[l.SelectedRow])
			}
			break done
		}
		ui.Render(l)
	}

	return selectedThings, false
}

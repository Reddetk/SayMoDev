// Command bc3mock is a local stand-in for BC#3 GET /content/programs* (test bench only).
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/google/uuid"
)

type lesson struct {
	LessonID      string `json:"lessonId"`
	OrderPosition int    `json:"orderPosition"`
	LessonType    string `json:"lessonType"`
	Title         string `json:"title"`
	ContentURL    string `json:"contentUrl"`
}

type program struct {
	ProgramID    string     `json:"programId"`
	Title        string     `json:"title"`
	Classifier   classifier `json:"classifier"`
	Version      int        `json:"version"`
	TotalLessons int        `json:"totalLessons"`
	Lessons      []lesson   `json:"lessons,omitempty"`
}

type classifier struct {
	Difficulty  string `json:"difficulty"`
	AphasiaType string `json:"aphasiaType"`
}

var (
	namespace = uuid.MustParse("6f1c2a44-4d7e-4a1e-9a1d-6f2b8e4c9a10")
	programs  []program
)

func main() {
	addr := flag.String("addr", envOr("BC3_ADDR", ":8081"), "listen address")
	aphasia := flag.String("aphasia", envOr("BC3_APHASIA", "motor_aphasia"), "aphasia type served by the stub")
	lessonsPerProgram := flag.Int("lessons", 3, "lessons per program")
	flag.Parse()

	for _, diff := range []string{"easy", "medium", "hard"} {
		p := program{
			ProgramID:    uuid.NewSHA1(namespace, []byte(diff)).String(),
			Title:        "Rehabilitation program (" + diff + ")",
			Classifier:   classifier{Difficulty: diff, AphasiaType: *aphasia},
			Version:      1,
			TotalLessons: *lessonsPerProgram,
		}
		for i := 1; i <= *lessonsPerProgram; i++ {
			lt := "regular"
			if i == *lessonsPerProgram {
				lt = "creative"
			}
			p.Lessons = append(p.Lessons, lesson{
				LessonID:      uuid.NewSHA1(namespace, []byte(diff+"-"+string(rune('0'+i)))).String(),
				OrderPosition: i,
				LessonType:    lt,
				Title:         "Lesson " + string(rune('0'+i)) + " (" + diff + ")",
				ContentURL:    "https://storage.yandexcloud.net/saymo-content/" + diff + "/" + string(rune('0'+i)) + ".pdf",
			})
		}
		programs = append(programs, p)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("/content/programs", func(w http.ResponseWriter, r *http.Request) {
		diff := r.URL.Query().Get("difficulty")
		aph := r.URL.Query().Get("aphasia_type")
		var list []program
		for _, p := range programs {
			if diff != "" && p.Classifier.Difficulty != diff {
				continue
			}
			if aph != "" && p.Classifier.AphasiaType != aph {
				continue
			}
			list = append(list, p)
		}
		writeJSON(w, list)
	})
	mux.HandleFunc("/content/programs/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/content/programs/")
		for _, p := range programs {
			if p.ProgramID == id {
				writeJSON(w, p)
				return
			}
		}
		http.NotFound(w, r)
	})

	log.Printf("bc3mock listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

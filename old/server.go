package main

/* Ideas
 * Possibly sort words
 */

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"
	"github.com/spf13/cobra"
)

var (
  ip string = "127.0.0.1"
	port uint16

	db   *sql.DB

  tmplsPath string
	tmpl *template.Template
)

func main() {
  cmd := cobra.Command{
    Use: "lively-langs",
    Run: run,
    DisableFlagsInUseLine: true,
  }

  flags := cmd.Flags()
  flags.String("db", "lively-langs.db", "Path to database")
  flags.String("static", "./static", "Path to static dir")
  flags.StringVar(&tmplsPath, "templates", "./templates", "Path to templates dir")
  flags.IP("ip", net.IPv4(127, 0, 0, 1), "IP address to run on")
  flags.Uint16("port", 8000, "Port to run on")

  if err := cmd.Execute(); err != nil {
    log.Fatal(err)
  }
}

func run(cmd *cobra.Command, _ []string) {
  flags := cmd.Flags()

  ip, _ := flags.GetIP("ip")
  port, _ := flags.GetUint16("port")
  dbPath, _ := flags.GetString("db")
  staticPath, _ := flags.GetString("static")

	var err error
	db, err = sql.Open("sqlite3", dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
  if _, err := db.Exec(createTablesStmt); err != nil {
    log.Fatal("error initializing DB: ", err)
  }

  if err := parse(); err != nil {
    log.Fatal("error parsing templates: ", err)
  }

  r := http.NewServeMux()
	fs := http.FileServer(http.Dir(staticPath))
	r.Handle("/static/", http.StripPrefix("/static", fs))
	r.HandleFunc("/", pageHandler)
	r.HandleFunc("/api", apiHandler)

  srvr := &http.Server{
    Handler: r,
  }

  addr := &net.TCPAddr{IP: ip, Port: int(port)}
  ln, err := net.ListenTCP("tcp", addr)
  if err != nil {
    log.Fatalf("error starting listener on %s: %v", addr, err)
  }

  log.Printf("listening on %s", addr)
	log.Fatal(srvr.Serve(ln))
}

func parse() (err error) {
  path := filepath.Join(tmplsPath, "index.html")
	tmpl, err = template.ParseFiles(path)
  return
}

func pageHandler(w http.ResponseWriter, r *http.Request) {
	tmpl.Execute(w, nil)
}

func apiHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		log.Println(err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header()["Content-Type"] = []string{"application/json"}
	encoder := json.NewEncoder(w)
	// "word" could be a sentence, hence why Join() is used
	word := strings.Join(r.Form["word"], " ")
	if word == "" {
		if err := encoder.Encode(Error{"must provide word"}); err != nil {
			log.Println(err)
		}
		return
	}
	// Method should only be post if an admin is adding/updating a word
	// or user is suggesting a new word or update to existing one
	if r.Method == http.MethodPost {
		// methods should only be "suggestion" or "admin"
		// "suggestion" is a user suggestion that will be sent to admins
		// "admin" is used for adding/updating words directly; requires credentials
		// "parse" is used to parse the page templates again; requires credentials
		method := r.FormValue("method")
		oldDef := strings.Join(r.Form["old"], " ")
		newDef := strings.Join(r.Form["new"], " ")
		if method == "suggestion" {
      /* TODO: Handle suggestions */
		} else if method == "admin" {
			// Search for admin in database
			email, password := r.FormValue("email"), r.FormValue("password")
			row := db.QueryRow(fmt.Sprintf(`SELECT * FROM users WHERE email="%s"`, email))
			var e, p string
			if err := row.Scan(&e, &p); errors.Is(err, sql.ErrNoRows) {
				// ErrNoRows if admin doesn't exist
				if err := encoder.Encode(Error{"invalid username or password"}); err != nil {
					log.Println(err)
				}
				return
			} else if err != nil {
				log.Println(err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			/* Hash password */
			if p != password {
				if err := encoder.Encode(Error{"invalid username or password"}); err != nil {
					log.Println(err)
				}
				return
			}
			/* Create, Update, or Delete word */
			println(oldDef, newDef)
		} else if method == "parse" {
			email, password := r.FormValue("email"), r.FormValue("password")
			row := db.QueryRow(fmt.Sprintf(`SELECT * FROM users WHERE email="%s"`, email))
			var e, p string
			if err := row.Scan(&e, &p); errors.Is(err, sql.ErrNoRows) {
				// ErrNoRows if admin doesn't exist
				if err := encoder.Encode(Error{"invalid username or password"}); err != nil {
					log.Println(err)
				}
				return
			} else if err != nil {
				log.Println(err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			/* Hash password */
			if p != password {
				if err := encoder.Encode(Error{"invalid username or password"}); err != nil {
					log.Println(err)
				}
				return
			}
			parse()
		} else {
			if err := encoder.Encode(Error{"invalid method"}); err != nil {
				log.Println(err)
				return
			}
		}
		return
	}
	// Form the query stmt
	stmt := `SELECT * FROM words`
	if word == "*" {
		stmt += fmt.Sprintf(` ORDER BY word`)
	} else {
		/* Better handle where stmt (possibly using "LIKE" or something similar) */
		stmt += fmt.Sprintf(` WHERE word="%s" ORDER BY def`, word)
	}
	rows, err := db.Query(stmt)
	if err != nil {
		log.Println(err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var def, gender string
	var entries []Entry
	for rows.Next() {
		if err := rows.Scan(&word, &def, &gender); err != nil {
			log.Println(err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		entries = append(entries, Entry{word, def, gender})
	}
	if err := encoder.Encode(entries); err != nil {
		log.Println(err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// Entry holds information for a word
type Entry struct {
	Word   string `json:"word"`
	Def    string `json:"def"`
	Gender string `json:"gender"`
}

// Error is an error struct for JSON
type Error struct {
	Error string `json:"error"`
}

const createTablesStmt = `
CREATE TABLE IF NOT EXISTS words (
  word TEXT NOT NULL,
  definition TEXT NOT NULL,
  gender TEXT
);
CREATE TABLE IF NOT EXISTS users (
  email TEXT NOT NULL UNIQUE,
  password TEXT NOT NULL
);
`

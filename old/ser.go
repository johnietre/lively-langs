// Update SELECT-LIKE stmt to handle
// Ex) if chico is in the db but not chica and you search for chica
// chico should come up
// Possibly use RegEx
// Figure out why http.Error does not redirect to an error page
// Finish "Not what you're looking for?" button/function
// After that, make it display "Still not what you're looking for?"
// Possibly use sqlite user functions rather than my own
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	_ "github.com/mattn/go-sqlite3"
	"html/template"
	"log"
	"net"
	"net/http"
	"strings"
	"sync" // Only going to be used while sqlite is used
)

// Struct for word entries
type entry struct {
	Word   string
	Def    string
	Gender string
	Action string // For API
}

// Struct containing the page data (used for HTML page and API data)
type PageData struct {
	Msg      string
	Entries  []entry
	NotFound bool
}

var (
	IP string = "localhost"
	WebPort string = ":8000"
	APIPort string = ":9000"
	lock    sync.Mutex
)

func WebpageServer() {
	fs := http.FileServer(http.Dir("./static/"))
	http.Handle("/static/", http.StripPrefix("/static", fs))
	http.HandleFunc("/", PageHandler)
	log.Fatal(http.ListenAndServe(IP+WebPort, nil))
}

// func main() {
//   log.Printf("Running on %s%s (API %s)", IP, WebPort, APIPort)
//   WebpageServer()
//   go APIServer()
// }

func checkErr(err error) bool {
	if err != nil {
		log.Println(err)
		return true // Return true if there was an error
	} else {
		return false
	}
}

func getDB() *sql.DB {
	db, err := sql.Open("sqlite3", "dict.db")
	if checkErr(err) {
		return nil
	}
	return db
}

func getPage(find string, like bool) PageData {
	var page PageData
	// If we are finding "LIKE" words, then the original word wasn't found
	page.NotFound = like
	db := getDB()
	if db == nil {
		page.Msg = "Database error"
		return page
	}
	defer db.Close()
	var stmt string
	if like { // Use this stmt if we are trying to "LIKE" words
		stmt = fmt.Sprintf(`SELECT * FROM words WHERE word LIKE "%%%s%%" ORDER BY word`, find)
	} else {
		if find == "*" { // Use this stmt if we want to get all words in the dictionary
			stmt = `SELECT * FROM words ORDER BY word`
		} else { // Use this if we are looking for a specific word
			stmt = fmt.Sprintf(`SELECT * FROM words WHERE word="%s" ORDER BY word`, find)
		}
	}
	rows, err := db.Query(stmt) // Execute the stmt and get the rows
	if checkErr(err) {
		page.Msg = "Database SELECT error"
		return page
	}
	defer rows.Close()
	var word, def, gender string
	for rows.Next() { // Loop through the rows, appending them to the page entries
		if !checkErr(rows.Scan(&word, &def, &gender)) {
			page.Entries = append(page.Entries, entry{word, def, gender, ""})
		}
	}
	return page
}

func addWord(word, def, gender string) {
	lock.Lock() // Lock the lock so the database can be safely written to
	defer lock.Unlock()
	db := getDB()
	if db == nil {
		return
	}
	defer db.Close()
	var stmt string
	stmt = fmt.Sprintf(`INSERT INTO words VALUES ("%s", "%s", "%s")`, word, def, gender)
	_, err := db.Exec(stmt)
	checkErr(err)
}

func hash(passwor string, login bool) string {
	return "hashed"
}

func register(fname, lname, username, email, password string) {
	db := getDB()
	if db == nil {
		return
	}
	defer db.Close()
	var stmt string
	stmt = fmt.Sprintf(`SELECT * FROM users WHERE email="%s"`)
	rows, err := db.Query(stmt)
	if checkErr(err) {
		return
	}
	for rows.Next() {
		println("Email address already used")
		rows.Close()
		return
	}
	hash(password, false)
	return
}

func login(username, email, password string) {
	return
}

func PageHandler(w http.ResponseWriter, r *http.Request) {
	var page PageData
	query := r.URL.Query()
	if r.Method == http.MethodGet {
		if len(query) != 0 { // If the page was not just loaded (a query was made)
			var word string
			if _, exists := query["all"]; exists == true {
				word = "*"
			} else {
				word = strings.ToLower(query["word"][0])
			}
			page = getPage(word, false)
			if len(page.Entries) == 0 && word != "*" && page.Msg == "" {
				page = getPage(word, true)
				if len(page.Entries) == 0 && page.Msg == "" {
					page = PageData{Msg: "No matches found", NotFound: true}
				} else {
					page.Msg = `Could not find any mathces for "` + word + `"`
				}
			}
		} else { // Page was just loaded (no queries have been made)
			page.Msg = "Search for words or phrases"
		}
	} else { // The user is adding a word to the dictionary
		if r.FormValue("submit") == "Add" { // If they click the "Add" button
			var word string = strings.ToLower(r.FormValue("word"))
			var def string = r.FormValue("def")
			var gender string = r.FormValue("gender")
			addWord(word, def, gender)
			page = getPage(word, false)
			if page.Msg == "" {
				page.Msg = "Word/Phrase added!"
			}
		} else { // If they click the "Cancel" button
			page = PageData{Msg: "Search for words or phrases"}
		}
	}
	ts, err := template.ParseFiles("../templates/index.html")
	if err != nil {
		log.Println(err)
		http.Error(w, "Internal Server Error... My bad", 500)
		return
	}
	if err = ts.Execute(w, page); err != nil {
		log.Println(err)
		http.Error(w, "Internal Server Error... My bad", 500)
	}
}

/* Server API for the app */

func APIServer() {
	// Create the socket listener
	ln, err := net.Listen("tcp", IP+APIPort)
	if err != nil {
		log.Panicln(err)
	}
	for {
		// Accept connections
		// Possibly use goroutine after accepting connenction to handle for things
		conn, err := ln.Accept()
		if checkErr(err) {
			conn.Close()
			continue
		}
		// Read the message and unmarshal it into an entry struct
		var msg []byte
		if _, err := conn.Read(msg); checkErr(err) {
			conn.Close()
			continue
		}
		var e *entry = &entry{}
		if checkErr(json.Unmarshal(msg, e)) {
			conn.Close()
			continue
		}
		if e.Action == "get" { // Handle queries for data
			var page PageData
			var word string = strings.ToLower(e.Word)
			page = getPage(word, false)
			if len(page.Entries) == 0 && word != "*" && page.Msg == "" {
				page = getPage(word, true)
				if len(page.Entries) == 0 && page.Msg == "" {
					page = PageData{Msg: "No matches found", NotFound: true}
				} else {
					page.Msg = "Could not find any mathces for " + word
				}
			}
			pb, err := json.Marshal(&page)
			if checkErr(err) {
				_, err = conn.Write(pb)
				checkErr(err)
			}
		} else { // Handle adding words
			addWord(e.Word, e.Def, e.Gender)
		}
		conn.Close()
	}
}

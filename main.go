package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/goccy/go-json"
	"github.com/joho/godotenv"
	"github.com/valyala/fastjson"
)

var (
	yearArg        = flag.String("year", "2023", "the year to scan")
	leaderboardArg = flag.String("leaderboard", "", "the leaderboard code to check")
	sessionArg     = flag.String("session", "", "session cookie to use to request the leaderboard")
	webhookURLArg  = flag.String("webhookURL", "", "webhook to post updates to")
)

var (
	webhook    = ""
	webhookURL *url.URL

	ChicagoTimeZone, _ = time.LoadLocation("America/Chicago")
	ordinals           = []string{"th", "st", "nd", "rd"}
)

type completionPartData struct {
	GetStarTimestamp int64 `json:"get_star_ts"`
	StarIndex        int64 `json:"star_index"`
}

type completionDayData struct {
	Part1 *completionPartData
	Part2 *completionPartData
}

type memberData struct {
	Name               string              `json:"name"`
	CompletionDayLevel []completionDayData `json:"-"`
	ID                 int                 `json:"id"`
	LocalScore         int                 `json:"local_score"`
	GlobalScore        int                 `json:"global_score"`
	Stars              int                 `json:"stars"`
	LastStarTs         int                 `json:"last_star_ts"`
}

type leaderboardData struct {
	Event   string       `json:"event"`
	Members []memberData `json:"-"`
	OwnerId int          `json:"owner_id"`
}

func main() {
	flag.Parse()
	dotenvErr := godotenv.Load()
	if dotenvErr != nil {
		log.Fatal("Error loading .env file")
	}

	session := *sessionArg
	if len(session) == 0 {
		session = os.Getenv("AOC_SESSION")
	}
	if len(session) == 0 {
		log.Fatalln("No session code provided. You must specify your session code as an argument or as an AOC_SESSION environment variable in either .env or defined in your environment to pull leaderboard info.")
	}

	leaderboardID := *leaderboardArg
	if len(leaderboardID) == 0 {
		leaderboardID = os.Getenv("AOC_LEADERBOARD")
	}
	if len(leaderboardID) == 0 {
		log.Fatalln("No leaderboard ID provided.")
	}

	webhook = *webhookURLArg
	if len(webhook) == 0 {
		webhook = os.Getenv("AOC_WEBHOOK")
	}
	if len(webhook) == 0 {
		log.Fatalln("No webhook URL provided.")
	}
	var webhookErr error
	webhookURL, webhookErr = url.Parse(webhook)
	if webhookErr != nil {
		log.Fatalln("Unable to parse given webhook", webhook, "to a URL:", webhookErr)
	}

	var p fastjson.Parser
	var lastRead int64
	var lastBody []byte

	cache, cacheErr := os.ReadFile(".cache.json")
	if cacheErr != nil {
		if !errors.Is(cacheErr, os.ErrNotExist) {
			log.Println("Error reading cached data, will pull fresh copy:", cacheErr)
		}
	} else {
		cacheObj, parseErr := p.ParseBytes(cache)
		if parseErr == nil {
			lastRead = cacheObj.GetInt64("last_read")
			lastBody = cacheObj.GetStringBytes("last_body")
		}
	}

	if time.Since(time.Unix(lastRead, 0)) < time.Minute*15 {
		fmt.Println("Too soon since the last request; doing nothing")
		return
	}
	currBody := lastBody

	currBody, downloadErr := downloadLeaderboardData(*yearArg, leaderboardID, session)
	if downloadErr != nil {
		log.Fatalln("Error downloading leaderboard data:", downloadErr)
	}

	lastRead = time.Now().Unix()
	jsonBytes, marshalErr := json.Marshal(map[string]any{"last_read": lastRead, "last_body": string(currBody)})
	if marshalErr != nil {
		log.Println("Failed to marshal last-read data into json:", marshalErr)
	} else {
		writeErr := os.WriteFile(".cache.json", jsonBytes, 0644)
		if writeErr != nil {
			log.Println("Failed to save cached data:", writeErr)
		}
	}

	lastLeaderboard, lastLeaderboardErr := buildLeaderboard(lastBody)
	if lastLeaderboardErr != nil {
		log.Fatalln("Error building leaderboard from cached body:", lastLeaderboardErr)
	}
	leaderboard, leaderboardErr := buildLeaderboard(currBody)
	if leaderboardErr != nil {
		log.Fatalln("Error building leaderboard from downloaded body:", leaderboardErr)
	}

	for _, member := range leaderboard.Members {
		lastMember := arrayFind(lastLeaderboard.Members, func(m memberData) bool { return m.ID == member.ID })
		if lastMember == nil {
			// todo: report if they've already got stars on the year
			nErr := sendNotification(fmt.Sprintf("A new challenger has appeared! Welcome, %s, to [the leaderboard](https://adventofcode.com/%s/leaderboard/private/view/%s)!", member.Name, *yearArg, leaderboardID))
			if nErr != nil {
				log.Printf("Error sending new-challenger notification to the leaderboard for %s: %v\n", member.Name, nErr)
			}

			continue
		}

		for idx, day := range member.CompletionDayLevel {
			totalStars := getTotalStars(&member)
			totalStarsPlural := "s"
			if totalStars == 1 {
				totalStarsPlural = ""
			}

			// todo: probably want to batch these for delivery later so we can sort by completion rank/time
			if day.Part1 != nil && lastMember.CompletionDayLevel[idx].Part1 == nil {
				completionTime := time.Unix(day.Part1.GetStarTimestamp, 0).In(ChicagoTimeZone).Format("3:04:05pm")
				rank := getCompletionRank(&leaderboard, &member, idx, 1) + 1
				ordinal := getOrdinal(rank)
				sendNotification(fmt.Sprintf(
					"%s completed day %d part 1 %d%s on [the leaderboard](https://adventofcode.com/%s/leaderboard/private/view/%s) at %s! %s now has %d star%s on the year.",
					member.Name,
					idx+1,
					rank,
					ordinal,
					*yearArg,
					leaderboardID,
					completionTime,
					member.Name,
					totalStars,
					totalStarsPlural,
				))
			}
			if day.Part2 != nil && lastMember.CompletionDayLevel[idx].Part2 == nil {
				completionTime := time.Unix(day.Part2.GetStarTimestamp, 0).In(ChicagoTimeZone).Format("3:04:05pm")
				rank := getCompletionRank(&leaderboard, &member, idx, 2) + 1
				ordinal := getOrdinal(rank)
				sendNotification(fmt.Sprintf(
					"%s completed day %d part 2 %d%s on [the leaderboard](https://adventofcode.com/%s/leaderboard/private/view/%s) at %s! %s now has %d star%s on the year.",
					member.Name,
					idx+1,
					rank,
					ordinal,
					*yearArg,
					leaderboardID,
					completionTime,
					member.Name,
					totalStars,
					totalStarsPlural,
				))
			}
		}
	}
}

func getTotalStars(member *memberData) int {
	total := 0
	for _, day := range member.CompletionDayLevel {
		if day.Part1 != nil {
			total++
		}
		if day.Part2 != nil {
			total++
		}
	}

	return total
}

func getCompletionRank(leaderboard *leaderboardData, inMember *memberData, dayIdx int, partNum int) int {
	targetTime := inMember.CompletionDayLevel[dayIdx].Part1.GetStarTimestamp
	if partNum != 1 {
		targetTime = inMember.CompletionDayLevel[dayIdx].Part2.GetStarTimestamp
	}

	numAhead := 0
	for _, member := range leaderboard.Members {
		if member.ID == inMember.ID {
			continue
		}

		part := member.CompletionDayLevel[dayIdx].Part1
		if partNum != 1 {
			part = member.CompletionDayLevel[dayIdx].Part2
		}
		if part == nil {
			continue
		}

		if part.GetStarTimestamp < targetTime {
			numAhead++
		}
	}

	return numAhead
}

func getOrdinal(n int) string {
	v := n % 100
	if v >= 20 && len(ordinals) > (v-20)%10 {
		return ordinals[(v-20)%10]
	}
	if len(ordinals) > v {
		return ordinals[v]
	}
	return ordinals[0]
}

func downloadLeaderboardData(year, leaderboardID, sessionID string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("https://adventofcode.com/%s/leaderboard/private/view/%s.json", year, leaderboardID), nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request for leaderboard: %w", err)
	}

	req.AddCookie(&http.Cookie{
		Name:     "session",
		Value:    sessionID,
		Path:     "/",
		Domain:   ".adventofcode.com",
		Secure:   true,
		HttpOnly: true,
	})

	client := http.DefaultClient
	resp, reqErr := client.Do(req)
	if reqErr != nil {
		return nil, fmt.Errorf("error attempting to download leaderboard: %w", reqErr)
	}

	read, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, fmt.Errorf("error reading response body: %w", readErr)
	}

	return read, nil
}

func buildLeaderboard(body []byte) (leaderboardData, error) {
	var leaderboard leaderboardData
	marshalErr := json.Unmarshal(body, &leaderboard)
	if marshalErr != nil {
		return leaderboard, fmt.Errorf("error unmarshaling string into leaderboardData: %w", marshalErr)
	}

	jsonObj, parseErr := fastjson.ParseBytes(body)
	if parseErr != nil {
		return leaderboard, fmt.Errorf("error parsing string into json: %w", parseErr)
	}

	members := jsonObj.GetObject("members")
	members.Visit(func(key []byte, memberVal *fastjson.Value) {
		var member memberData
		json.Unmarshal([]byte(memberVal.String()), &member)
		member.CompletionDayLevel = make([]completionDayData, 25)

		completionObj := memberVal.GetObject("completion_day_level")
		completionObj.Visit(func(completionKey []byte, completionDay *fastjson.Value) {
			memberCompletionObj := completionDayData{}

			completionDayObj, _ := completionDay.Object()
			completionDayObj.Visit(func(completionPartKey []byte, completionPartVal *fastjson.Value) {
				var completionPart completionPartData
				json.Unmarshal([]byte(completionPartVal.String()), &completionPart)
				if string(completionPartKey) == "1" {
					memberCompletionObj.Part1 = &completionPart
				} else {
					memberCompletionObj.Part2 = &completionPart
				}
			})

			completionDayNum, _ := strconv.Atoi(string(completionKey))
			member.CompletionDayLevel[completionDayNum-1] = memberCompletionObj
		})

		leaderboard.Members = append(leaderboard.Members, member)
	})

	return leaderboard, nil
}

func sendNotification(content string) error {
	b, _ := json.Marshal(struct {
		Text string `json:"text"`
	}{
		Text: content,
	})
	resp, err := http.DefaultClient.Post(webhookURL.String(), "application/json", bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("error POSTing to webhook: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code %d", resp.StatusCode)
	}

	return nil
}

func arrayContains[T any](array []T, pred func(val T) bool) bool {
	for _, v := range array {
		if pred(v) {
			return true
		}
	}

	return false
}

func arrayFind[T any](array []T, pred func(val T) bool) *T {
	for _, v := range array {
		if pred(v) {
			return &v
		}
	}

	return nil
}

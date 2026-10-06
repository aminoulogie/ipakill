package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Soma's data, read out of the synced records (somasync.go) and changed the way Soma's own
// store changes it (src/lib/store.ts), so the PC and the phone see the same thing:
//   habits#<id>, habits@order  a habit: name, colour, history{date: done}, steps, stepLog
//   todos#<id>, todos@order    a to-do: text, done, date, scope "day"|"week", due, slot
//   dayPlans/<date>            the day's time blocks: label, hours, colour, start
//   reading/<date>             minutes read
//   history/<date>             a training session: split, durationFormatted, totalSets...
//   nutrition/<date>           food items (cals, p, c, f), goals, water, bodyWeight

type obj = map[string]any

func somaDate(t time.Time) string { return t.In(displayZone).Format("2006-01-02") }
func somaToday() string           { return somaDate(time.Now()) }

// mondayOf is the Monday starting the week date falls in (todos.ts).
func mondayOf(date string) string {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	return t.AddDate(0, 0, -((int(t.Weekday()) + 6) % 7)).Format("2006-01-02")
}

func somaObj(key string) obj {
	var o obj
	if s := soma.get(key); s != "" {
		json.Unmarshal([]byte(s), &o)
	}
	return o
}

func num(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	}
	return 0
}

func str(v any) string { s, _ := v.(string); return s }

// list is the items of an id'd array field, in its order (items the order doesn't name last).
func somaList(field string) []obj {
	var order []string
	json.Unmarshal([]byte(soma.get(field+"@order")), &order)
	seen := map[string]bool{}
	var out []obj
	for _, id := range order {
		if o := somaObj(field + "#" + id); o != nil && !seen[id] {
			out = append(out, o)
			seen[id] = true
		}
	}
	var rest []string
	for _, k := range soma.keysWith(field + "#") {
		if id := strings.TrimPrefix(k, field+"#"); !seen[id] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		if o := somaObj(k); o != nil {
			out = append(out, o)
		}
	}
	return out
}

func somaPut(key string, o any) {
	b, _ := json.Marshal(o)
	soma.put(key, string(b))
}

// somaNewID is Soma's newId(): the time in base 36 and six random base-36 characters.
func somaNewID() string {
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, 6)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(36))
		b[i] = digits[n.Int64()]
	}
	return strconv.FormatInt(time.Now().UnixMilli(), 36) + string(b)
}

// --- habits ---------------------------------------------------------------------------------

type somaHabit struct {
	ID, Name, Color string
	o               obj
}

func somaHabits() []somaHabit {
	var out []somaHabit
	for _, o := range somaList("habits") {
		out = append(out, somaHabit{ID: str(o["id"]), Name: str(o["name"]), Color: str(o["color"]), o: o})
	}
	return out
}

func (h somaHabit) done(date string) bool {
	hist, _ := h.o["history"].(obj)
	b, _ := hist[date].(bool)
	return b
}

// ramp: a number that moves every day; ticked on the phone or the PC, where it's set.
func (h somaHabit) ramp() bool { return h.o["ramp"] != nil }

// somaToggleHabit is Soma's toggleHabit for today (setAll in habit-steps.ts).
func somaToggleHabit(id string) error {
	o := somaObj("habits#" + id)
	if o == nil {
		return fmt.Errorf("no such habit")
	}
	if o["ramp"] != nil {
		return fmt.Errorf("this habit counts an amount: log it in Soma on the phone or the PC")
	}
	date := somaToday()
	hist, _ := o["history"].(obj)
	if hist == nil {
		hist = obj{}
	}
	on, _ := hist[date].(bool)
	on = !on
	hist[date] = on
	o["history"] = hist
	if steps, ok := o["steps"].([]any); ok && len(steps) > 0 {
		log, _ := o["stepLog"].(obj)
		if log == nil {
			log = obj{}
		}
		if on {
			day := obj{}
			for _, s := range steps {
				if so, ok := s.(obj); ok {
					day[str(so["id"])] = max(num(so["target"]), 1)
				}
			}
			log[date] = day
		} else {
			delete(log, date)
		}
		o["stepLog"] = log
	}
	somaPut("habits#"+id, o)
	return nil
}

// --- to-dos ---------------------------------------------------------------------------------

type somaTodo struct {
	ID, Text, Date, Scope, Due string
	Done, Cleared              bool
	SlotDate                   string
	SlotStart, SlotMins        int
}

func somaTodos() []somaTodo {
	var out []somaTodo
	for _, o := range somaList("todos") {
		t := somaTodo{ID: str(o["id"]), Text: str(o["text"]), Date: str(o["date"]), Scope: str(o["scope"]), Due: str(o["due"])}
		t.Done, _ = o["done"].(bool)
		t.Cleared, _ = o["cleared"].(bool)
		if t.Scope != "week" {
			t.Scope = "day"
		}
		if s, ok := o["slot"].(obj); ok {
			t.SlotDate, t.SlotStart, t.SlotMins = str(s["date"]), int(num(s["start"])), int(num(s["mins"]))
		}
		out = append(out, t)
	}
	return out
}

// active: on the list you'd see on date (todos.ts isActive).
func (t somaTodo) active(date string) bool {
	if t.Cleared {
		return false
	}
	if t.Scope == "week" {
		return mondayOf(t.Date) == mondayOf(date)
	}
	return t.Date == date
}

// on: belongs to the day date in the calendar (its day, its time slot, or its deadline).
func (t somaTodo) on(date string) bool {
	return (t.Scope == "day" && t.Date == date) || t.SlotDate == date || t.Due == date
}

func somaToggleTodo(id string) {
	o := somaObj("todos#" + id)
	if o == nil {
		return
	}
	d, _ := o["done"].(bool)
	o["done"] = !d
	somaPut("todos#"+id, o)
}

// somaAddTodo is Soma's addTodo (on date, which is today unless planned from the calendar).
func somaAddTodo(text, scope, date string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	id := somaNewID()
	somaPut("todos#"+id, obj{"id": id, "text": text, "done": false, "date": date, "scope": scope})
	var order []string
	for _, t := range somaList("todos") {
		order = append(order, str(t["id"]))
	}
	somaPut("todos@order", order)
}

// --- reading, plans, reports ----------------------------------------------------------------

func somaReading(date string) int { return int(num(jsonValue(soma.get("reading/" + date)))) }

func jsonValue(s string) any {
	var v any
	json.Unmarshal([]byte(s), &v)
	return v
}

func somaAddReading(date string, mins int) {
	n := min(max(somaReading(date)+mins, 0), 24*60)
	if n == 0 {
		soma.put("reading/"+date, "")
		return
	}
	soma.put("reading/"+date, strconv.Itoa(n))
}

type somaBlock struct {
	Label, Color string
	Start, Hours float64 // hours from midnight; the start of the day's ring
}

// somaPlan is the day's blocks with their clock times: one block may be pinned to a time
// (day-plan.ts: at most one carries start), and the rest follow it around the 24 hours.
func somaPlan(date string) []somaBlock {
	var raw []obj
	json.Unmarshal([]byte(soma.get("dayPlans/"+date)), &raw)
	if len(raw) == 0 {
		return nil
	}
	first := 0.0
	acc := 0.0
	for _, b := range raw {
		if s, ok := b["start"].(float64); ok {
			first = s - acc
			break
		}
		acc += num(b["hours"])
	}
	var out []somaBlock
	at := first
	for _, b := range raw {
		h := num(b["hours"])
		out = append(out, somaBlock{Label: str(b["label"]), Color: str(b["color"]), Start: wrap24(at), Hours: h})
		at += h
	}
	return out
}

func wrap24(h float64) float64 {
	for h < 0 {
		h += 24
	}
	for h >= 24 {
		h -= 24
	}
	return h
}

func clockOf(h float64) string {
	m := int(h*60+0.5) % (24 * 60)
	return fmt.Sprintf("%02d:%02d", m/60, m%60)
}

type somaSession struct {
	Date, Split, Duration string
	Sets                  int
	Volume, Calories      float64
}

func somaTraining(date string) *somaSession {
	o := somaObj("history/" + date)
	if o == nil {
		return nil
	}
	return &somaSession{Date: date, Split: str(o["split"]), Duration: str(o["durationFormatted"]),
		Sets: int(num(o["totalSets"])), Volume: num(o["totalVol"]), Calories: num(o["caloriesBurned"])}
}

type somaFood struct {
	Date                       string
	Cals, P, C, F              float64
	GoalCals, GoalP, Water, Kg float64
	Logged                     bool
}

func somaNutrition(date string) somaFood {
	o := somaObj("nutrition/" + date)
	n := somaFood{Date: date}
	if o == nil {
		return n
	}
	items, _ := o["items"].([]any)
	for _, it := range items {
		if f, ok := it.(obj); ok {
			n.Cals += num(f["cals"])
			n.P += num(f["p"])
			n.C += num(f["c"])
			n.F += num(f["f"])
		}
	}
	n.Logged = len(items) > 0
	if g, ok := o["goals"].(obj); ok {
		n.GoalCals, n.GoalP = num(g["cals"]), num(g["protein"])
	}
	n.Water, n.Kg = num(o["water"]), num(o["bodyWeight"])
	return n
}

// days is the n days ending on date, oldest first.
func days(date string, n int) []string {
	t, _ := time.Parse("2006-01-02", date)
	out := make([]string, n)
	for i := range out {
		out[i] = t.AddDate(0, 0, i-n+1).Format("2006-01-02")
	}
	return out
}

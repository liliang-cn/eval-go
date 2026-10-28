package tools

import (
	"fmt"
	"math"
	"net/mail"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Every name, number and address below is invented.

var defaultUser = map[string]any{
	"name": "Lin Wei", "name_zh": "林薇", "home_city": "Shanghai",
	"home_address": "88 Yongkang Road, Xuhui, Shanghai", "work_address": "Tower B, 500 Shibo Avenue, Pudong, Shanghai",
	"timezone": "Asia/Shanghai", "email": "lin.wei@example.com", "phone": "+86 138 0000 0001",
	"languages": []string{"zh", "en"}, "units": "metric", "currency": "CNY",
}

func lifeDefaults(e *Env) map[string]any {
	return map[string]any{
		"contacts": []map[string]any{
			{"id": "ct_1", "name": "Mia Chen", "name_zh": "陈米娅", "relationship": "sister 妹妹", "phone": "+86 138 0013 4521", "email": "mia.chen@example.com", "wechat": "miachen_88"},
			{"id": "ct_2", "name": "Zhou Hao", "name_zh": "周浩", "relationship": "colleague 同事", "phone": "+86 139 1720 8834", "email": "zhou.hao@acme.example", "wechat": "zhouhao_work"},
			{"id": "ct_3", "name": "Mom", "name_zh": "妈妈", "relationship": "mother 妈妈", "phone": "+86 137 0001 2299", "wechat": "lin_mama"},
			{"id": "ct_4", "name": "Dr. Wang Li", "name_zh": "王丽医生", "relationship": "family doctor 家庭医生", "phone": "+86 21 6458 1200", "email": "clinic.wang@health.example"},
			{"id": "ct_5", "name": "Sam Patel", "relationship": "friend 朋友", "phone": "+1 415 555 0142", "email": "sam.patel@example.org", "whatsapp": "+14155550142"},
			{"id": "ct_6", "name": "Zhou Hong", "name_zh": "周红", "relationship": "neighbour 邻居", "phone": "+86 136 5521 0937", "email": "zhou.hong@example.com", "wechat": "hong_zhou"},
			{"id": "ct_7", "name": "Li Na", "name_zh": "李娜", "relationship": "landlord 房东", "phone": "+86 135 2468 1357", "wechat": "lina_fangdong"},
			{"id": "ct_8", "name": "Kenji Sato", "relationship": "client 客户", "email": "k.sato@sato-trading.example", "phone": "+81 3 5555 0199"},
		},
		"devices": []map[string]any{
			{"id": "dev_1", "name": "Living room light", "name_zh": "客厅灯", "room": "living room", "type": "light", "state": "off"},
			{"id": "dev_2", "name": "Bedroom AC", "name_zh": "卧室空调", "room": "bedroom", "type": "ac", "state": "off", "target_c": 26, "mode": "auto"},
			{"id": "dev_3", "name": "Living room AC", "name_zh": "客厅空调", "room": "living room", "type": "ac", "state": "off", "target_c": 26, "mode": "auto"},
			{"id": "dev_4", "name": "Kettle plug", "name_zh": "水壶插座", "room": "kitchen", "type": "plug", "state": "off"},
			{"id": "dev_5", "name": "Bedroom light", "name_zh": "卧室灯", "room": "bedroom", "type": "light", "state": "on"},
		},
		"settings": map[string]any{"language": "zh", "units": "metric", "default_reminder_minutes": "10", "morning_briefing_time": "07:30", "voice_replies": "off", "currency": "CNY", "do_not_disturb": false},
		"files": []map[string]any{
			{"path": "/Documents/lease-2026.pdf.txt", "content": "Lease for 88 Yongkang Road. Monthly rent CNY 6,800, due on the 5th. Deposit CNY 13,600.", "modified": "2026-03-01T10:00:00+08:00"},
			{"path": "/Documents/passport-scan.txt", "content": "Passport number E1234**** (redacted). Expires 2031-06-30.", "modified": "2025-11-12T09:00:00+08:00"},
		},
	}
}

func runLife(c *call) any {
	switch c.name {
	// --- time ---
	case "get_current_time":
		now := c.env.now()
		if tz := c.str("timezone"); tz != "" {
			loc, err := time.LoadLocation(tz)
			if err != nil {
				return errResult("INVALID_ARGUMENT", false, "unknown time zone %q (use an IANA name like Europe/Paris)", tz)
			}
			now = now.In(loc)
		}
		return map[string]any{"now": now.Format(time.RFC3339), "weekday": now.Weekday().String(), "timezone": now.Location().String()}
	case "convert_timezone":
		from, err1 := time.LoadLocation(c.str("from_tz"))
		to, err2 := time.LoadLocation(c.str("to_tz"))
		if err1 != nil || err2 != nil {
			return errResult("INVALID_ARGUMENT", false, "unknown time zone (use IANA names like Asia/Tokyo)")
		}
		t, err := parseTime(c.str("time"), from)
		if err != nil {
			return errResult("INVALID_ARGUMENT", false, "time %q is not a date-time", c.str("time"))
		}
		return map[string]any{"from": t.In(from).Format(time.RFC3339), "to": t.In(to).Format(time.RFC3339), "to_weekday": t.In(to).Weekday().String()}
	case "date_calc":
		d, err := time.ParseInLocation("2006-01-02", c.str("date"), shanghai)
		if err != nil {
			return errResult("INVALID_ARGUMENT", false, "date must be YYYY-MM-DD")
		}
		switch c.str("operation") {
		case "add_days":
			n, ok := c.num("days")
			if !ok {
				return errResult("INVALID_ARGUMENT", false, "add_days needs days")
			}
			r := d.AddDate(0, 0, int(n))
			return map[string]any{"date": r.Format("2006-01-02"), "weekday": r.Weekday().String()}
		case "days_between":
			d2, err := time.ParseInLocation("2006-01-02", c.str("date2"), shanghai)
			if err != nil {
				return errResult("INVALID_ARGUMENT", false, "days_between needs date2 as YYYY-MM-DD")
			}
			return map[string]any{"days": int(math.Round(d2.Sub(d).Hours() / 24))}
		default:
			return map[string]any{"date": c.str("date"), "weekday": d.Weekday().String()}
		}
	case "get_holidays":
		y, _ := c.num("year")
		if int(y) != 2026 {
			return map[string]any{"holidays": []any{}, "note": "only 2026 is loaded"}
		}
		return map[string]any{"holidays": holidays2026[c.str("country")]}

	// --- weather ---
	case "get_weather":
		w := weatherFor(c.str("city"), 0)
		w["as_of"] = c.env.now().Add(-12 * time.Minute).Format(time.RFC3339)
		return imperial(c, w)
	case "get_weather_forecast":
		n, _ := c.num("days")
		var days []any
		for i := 0; i < int(n); i++ {
			w := weatherFor(c.str("city"), i)
			w["date"] = c.env.now().AddDate(0, 0, i).Format("2006-01-02")
			delete(w, "temp_c")
			days = append(days, imperial(c, w))
		}
		return map[string]any{"city": c.str("city"), "days": days}
	case "get_air_quality":
		h := hashOf(strings.ToLower(c.str("city")))
		aqi := 20 + int(h%140)
		return map[string]any{"city": c.str("city"), "aqi": aqi, "level": aqiLevel(aqi), "main_pollutant": []string{"PM2.5", "PM10", "O3"}[h%3]}

	// --- notes ---
	case "create_note":
		folder := c.str("folder")
		if folder == "" {
			folder = "Notes"
		}
		return map[string]any{"ok": true, "note": c.create("notes", "note", map[string]any{"folder": folder})}
	case "list_notes":
		var out []map[string]any
		for _, n := range c.items("notes") {
			if f := c.str("folder"); f != "" && !strings.EqualFold(fmt.Sprint(n["folder"]), f) {
				continue
			}
			if tag := c.str("tag"); tag != "" && !strings.Contains(strings.ToLower(fmt.Sprint(n["tags"])), strings.ToLower(tag)) {
				continue
			}
			out = append(out, brief(n, "id", "title", "folder", "tags", "created_at"))
		}
		reverse(out)
		return c.page("notes", out, 10)
	case "search_notes":
		var out []map[string]any
		for _, n := range c.items("notes") {
			if looseMatch(n, c.str("query"), "title", "content", "tags") {
				out = append(out, n)
			}
		}
		return map[string]any{"notes": nonNil(out)}
	case "get_note":
		n, err := c.byID("notes", "note_id")
		if err != nil {
			return err
		}
		return map[string]any{"note": n}
	case "update_note", "append_to_note":
		n, err := c.byID("notes", "note_id")
		if err != nil {
			return err
		}
		if c.name == "append_to_note" {
			n["content"] = fmt.Sprint(n["content"]) + "\n" + c.str("text")
			n["updated_at"] = c.env.now().Format(time.RFC3339)
		} else {
			c.update(n, "note_id")
		}
		return map[string]any{"ok": true, "note": n}
	case "delete_note":
		return c.deleteBy("notes", "note_id")

	// --- to-dos ---
	case "create_todo":
		extra := map[string]any{"status": "open"}
		if !c.has("priority") {
			extra["priority"] = "normal"
		}
		if !c.has("list") {
			extra["list"] = "Inbox"
		}
		return map[string]any{"ok": true, "todo": c.create("todos", "todo", extra)}
	case "list_todos":
		status := c.str("status")
		if status == "" {
			status = "open"
		}
		var out []map[string]any
		for _, t := range c.items("todos") {
			if status != "all" && t["status"] != status {
				continue
			}
			if l := c.str("list"); l != "" && !strings.EqualFold(fmt.Sprint(t["list"]), l) {
				continue
			}
			out = append(out, t)
		}
		return map[string]any{"todos": nonNil(out)}
	case "complete_todo":
		t, err := c.byID("todos", "todo_id")
		if err != nil {
			return err
		}
		t["status"] = "done"
		t["completed_at"] = c.env.now().Format(time.RFC3339)
		return map[string]any{"ok": true, "todo": t}
	case "update_todo":
		t, err := c.byID("todos", "todo_id")
		if err != nil {
			return err
		}
		c.update(t, "todo_id")
		return map[string]any{"ok": true, "todo": t}
	case "delete_todo":
		return c.deleteBy("todos", "todo_id")

	// --- calendar ---
	case "create_event":
		start, _ := c.timeArg("start")
		end, _ := c.timeArg("end")
		if !end.After(start) {
			return errResult("INVALID_ARGUMENT", false, "end must be after start")
		}
		if bad := badEmails(c.args["attendees"]); bad != "" {
			return errResult("INVALID_ARGUMENT", false, "attendee %q is not an email address", bad)
		}
		ev := c.create("events", "evt", nil)
		res := map[string]any{"ok": true, "event": ev}
		if conflicts := c.overlapping(start, end, ev["id"]); len(conflicts) > 0 {
			res["warning"] = "overlaps existing events"
			res["conflicts"] = conflicts
		}
		return res
	case "list_events":
		var out []map[string]any
		for _, ev := range c.within(c.items("events"), "start") {
			if matchesQuery(ev, c.str("query"), "title", "location", "notes") {
				out = append(out, ev)
			}
		}
		sortByTime(out, "start")
		return map[string]any{"events": nonNil(out)}
	case "update_event":
		ev, err := c.byID("events", "event_id")
		if err != nil {
			return err
		}
		if bad := badEmails(c.args["attendees"]); bad != "" {
			return errResult("INVALID_ARGUMENT", false, "attendee %q is not an email address", bad)
		}
		c.update(ev, "event_id")
		s, _ := itemTime(ev, "start")
		e, _ := itemTime(ev, "end")
		if !e.After(s) {
			return errResult("INVALID_ARGUMENT", false, "end must be after start (start %s, end %s)", ev["start"], ev["end"])
		}
		return map[string]any{"ok": true, "event": ev}
	case "delete_event":
		return c.deleteBy("events", "event_id")
	case "find_free_slots":
		return c.freeSlots()

	// --- reminders ---
	case "create_reminder":
		at, _ := c.timeArg("remind_at")
		if at.Before(c.env.now()) {
			return errResult("INVALID_ARGUMENT", false, "remind_at %s is in the past (now is %s)", c.str("remind_at"), c.env.now().Format(time.RFC3339))
		}
		extra := map[string]any{"status": "pending"}
		if !c.has("repeat") {
			extra["repeat"] = "none"
		}
		var same []map[string]any
		for _, r := range c.items("reminders") {
			if t, ok := itemTime(r, "remind_at"); ok && t.Equal(at) && r["status"] == "pending" {
				same = append(same, brief(r, "id", "title", "remind_at"))
			}
		}
		res := map[string]any{"ok": true, "reminder": c.create("reminders", "rem", extra)}
		if len(same) > 0 {
			res["warning"] = "another pending reminder is already set for this exact time"
			res["existing"] = same
		}
		return res
	case "list_reminders":
		status := c.str("status")
		if status == "" {
			status = "pending"
		}
		var out []map[string]any
		for _, r := range c.within(c.items("reminders"), "remind_at") {
			if status == "all" || r["status"] == status {
				out = append(out, r)
			}
		}
		sortByTime(out, "remind_at")
		return map[string]any{"reminders": nonNil(out)}
	case "update_reminder":
		r, err := c.byID("reminders", "reminder_id")
		if err != nil {
			return err
		}
		if at, ok := c.timeArg("remind_at"); ok && at.Before(c.env.now()) {
			return errResult("INVALID_ARGUMENT", false, "remind_at is in the past")
		}
		c.update(r, "reminder_id")
		return map[string]any{"ok": true, "reminder": r}
	case "delete_reminder":
		return c.deleteBy("reminders", "reminder_id")
	case "snooze_reminder":
		r, err := c.byID("reminders", "reminder_id")
		if err != nil {
			return err
		}
		at, _ := itemTime(r, "remind_at")
		m, _ := c.num("minutes")
		r["remind_at"] = at.Add(time.Duration(m) * time.Minute).Format(time.RFC3339)
		return map[string]any{"ok": true, "reminder": r}

	// --- alarms ---
	case "create_alarm":
		extra := map[string]any{"enabled": true}
		if !c.has("repeat_days") {
			extra["repeat_days"] = []any{}
		}
		if d := c.str("date"); d != "" {
			day, _ := time.ParseInLocation("2006-01-02", d, shanghai)
			hm, _ := time.Parse("15:04", c.str("time"))
			at := day.Add(time.Duration(hm.Hour())*time.Hour + time.Duration(hm.Minute())*time.Minute)
			if at.Before(c.env.now()) {
				return errResult("INVALID_ARGUMENT", false, "alarm %s %s is in the past", d, c.str("time"))
			}
		}
		return map[string]any{"ok": true, "alarm": c.create("alarms", "alm", extra)}
	case "list_alarms":
		return map[string]any{"alarms": nonNil(c.items("alarms"))}
	case "update_alarm":
		a, err := c.byID("alarms", "alarm_id")
		if err != nil {
			return err
		}
		c.update(a, "alarm_id")
		return map[string]any{"ok": true, "alarm": a}
	case "delete_alarm":
		return c.deleteBy("alarms", "alarm_id")

	// --- timers ---
	case "start_timer":
		d, _ := c.num("duration_seconds")
		t := c.create("timers", "tmr", map[string]any{"ends_at": c.env.now().Add(time.Duration(d) * time.Second).Format(time.RFC3339), "status": "running"})
		return map[string]any{"ok": true, "timer": t}
	case "list_timers":
		return map[string]any{"timers": nonNil(c.items("timers"))}
	case "cancel_timer":
		t, err := c.byID("timers", "timer_id")
		if err != nil {
			return err
		}
		t["status"] = "cancelled"
		return map[string]any{"ok": true, "timer": t}

	// --- contacts ---
	case "search_contacts":
		var out []map[string]any
		for _, ct := range c.items("contacts") {
			if matchesQuery(ct, c.str("query"), "name", "name_zh", "relationship", "email", "nickname") {
				out = append(out, ct)
			}
		}
		return map[string]any{"results": nonNil(out)}
	case "get_contact":
		ct, err := c.byID("contacts", "contact_id")
		if err != nil {
			return err
		}
		return map[string]any{"contact": ct}
	case "create_contact":
		return map[string]any{"ok": true, "contact": c.create("contacts", "ct", nil)}
	case "update_contact":
		ct, err := c.byID("contacts", "contact_id")
		if err != nil {
			return err
		}
		c.update(ct, "contact_id")
		return map[string]any{"ok": true, "contact": ct}

	// --- email ---
	case "send_email", "draft_email":
		if bad := badEmails(c.args["to"]); bad != "" {
			return errResult("INVALID_ARGUMENT", false, "recipient %q is not an email address", bad)
		}
		if bad := badEmails(c.args["cc"]); bad != "" {
			return errResult("INVALID_ARGUMENT", false, "cc %q is not an email address", bad)
		}
		if c.name == "draft_email" {
			return map[string]any{"ok": true, "draft": c.create("drafts", "dr", nil)}
		}
		return map[string]any{"ok": true, "status": "sent", "email": c.create("sent_emails", "sent", nil)}
	case "search_emails":
		var out []map[string]any
		for _, m := range c.items("inbox") {
			if !matchesQuery(m, c.str("query"), "subject", "body", "from") {
				continue
			}
			if f := c.str("from"); f != "" && !c.fromMatches(fmt.Sprint(m["from"]), f) {
				continue
			}
			if b, _ := c.args["unread_only"].(bool); b && m["read"] == true {
				continue
			}
			out = append(out, brief(m, "id", "from", "subject", "received_at", "read", "snippet"))
		}
		return c.page("emails", out, 10)
	case "read_email":
		m, err := c.byID("inbox", "email_id")
		if err != nil {
			return err
		}
		m["read"] = true
		return map[string]any{"email": m}
	case "reply_email":
		m, err := c.byID("inbox", "email_id")
		if err != nil {
			return err
		}
		sent := c.create("sent_emails", "sent", map[string]any{"to": []any{m["from"]}, "subject": "Re: " + fmt.Sprint(m["subject"]), "in_reply_to": m["id"]})
		return map[string]any{"ok": true, "status": "sent", "email": sent}

	// --- messaging ---
	case "send_sms":
		if !rePhone.MatchString(c.str("phone")) {
			return errResult("INVALID_ARGUMENT", false, "phone %q is not a phone number in international format", c.str("phone"))
		}
		return map[string]any{"ok": true, "status": "delivered", "message": c.create("sms_sent", "sms", nil)}
	case "send_chat_message":
		return map[string]any{"ok": true, "status": "sent", "message": c.create("chats_sent", "chat", nil)}
	case "read_messages":
		var out []map[string]any
		for _, m := range c.items("messages_in") {
			if a := c.str("app"); a != "" && m["app"] != a {
				continue
			}
			if f := c.str("from"); f != "" && !matchesQuery(m, f, "from", "from_name") {
				continue
			}
			out = append(out, m)
		}
		if n, ok := c.num("limit"); ok && int(n) < len(out) {
			out = out[len(out)-int(n):]
		}
		return map[string]any{"messages": nonNil(out)}

	// --- files ---
	case "list_files":
		folder := strings.TrimRight(c.str("folder"), "/")
		var out []map[string]any
		for _, f := range c.items("files") {
			p := fmt.Sprint(f["path"])
			if folder == "" || strings.HasPrefix(p, folder+"/") {
				out = append(out, map[string]any{"path": p, "size": len(fmt.Sprint(f["content"])), "modified": f["modified"]})
			}
		}
		return map[string]any{"files": nonNil(out)}
	case "read_file":
		f := c.file(c.str("path"))
		if f == nil {
			return errResult("NOT_FOUND", false, "no file at %s", c.str("path"))
		}
		return map[string]any{"path": f["path"], "content": f["content"], "modified": f["modified"]}
	case "write_file":
		f := c.file(c.str("path"))
		ow, _ := c.args["overwrite"].(bool)
		if f != nil && !ow {
			return errResult("ALREADY_EXISTS", false, "%s exists; pass overwrite=true to replace it", c.str("path"))
		}
		if f == nil {
			f = map[string]any{"path": c.str("path")}
			appendList(c.st, "files", f)
		}
		f["content"] = c.str("content")
		f["modified"] = c.env.now().Format(time.RFC3339)
		return map[string]any{"ok": true, "path": f["path"]}
	case "search_files":
		var out []map[string]any
		for _, f := range c.items("files") {
			if matchesQuery(f, c.str("query"), "path", "content") {
				out = append(out, map[string]any{"path": f["path"], "modified": f["modified"]})
			}
		}
		return map[string]any{"files": nonNil(out)}
	case "share_file":
		if c.file(c.str("path")) == nil {
			return errResult("NOT_FOUND", false, "no file at %s", c.str("path"))
		}
		if bad := badEmails(c.str("email")); bad != "" {
			return errResult("INVALID_ARGUMENT", false, "%q is not an email address", bad)
		}
		return map[string]any{"ok": true, "share": c.create("shares", "shr", nil)}
	case "delete_file":
		p := c.str("path")
		if c.file(p) == nil {
			return errResult("NOT_FOUND", false, "no file at %s", p)
		}
		var kept []any
		for _, v := range c.list("files") {
			if m, _ := v.(map[string]any); m["path"] != p {
				kept = append(kept, v)
			}
		}
		c.st["files"] = nonNilAny(kept)
		return map[string]any{"ok": true}

	// --- markets ---
	case "get_stock_quote":
		sym := strings.ToUpper(c.str("symbol"))
		q, ok := stocks[sym]
		if !ok {
			return errResult("NOT_FOUND", false, "unknown symbol %q", sym)
		}
		return map[string]any{"symbol": sym, "name": q.name, "price": q.price, "currency": q.cur, "change_pct": q.chg,
			"as_of": c.env.now().Add(-15 * time.Minute).Format(time.RFC3339), "delayed_minutes": 15}
	case "get_crypto_price":
		coin := strings.ToUpper(c.str("coin"))
		p, ok := crypto[coin]
		if !ok {
			return errResult("NOT_FOUND", false, "unknown coin %q", coin)
		}
		vs := c.str("vs_currency")
		if vs == "" {
			vs = "USD"
		}
		return map[string]any{"coin": coin, "price": round2(p * usdPer[vs]), "currency": vs, "as_of": c.env.now().Add(-1 * time.Minute).Format(time.RFC3339)}
	case "get_fx_rate":
		r := usdPer[c.str("quote")] / usdPer[c.str("base")]
		return map[string]any{"base": c.str("base"), "quote": c.str("quote"), "rate": math.Round(r*10000) / 10000, "as_of": c.env.now().Add(-5 * time.Minute).Format(time.RFC3339)}
	case "convert_currency":
		amt, _ := c.num("amount")
		r := usdPer[c.str("to")] / usdPer[c.str("from")]
		return map[string]any{"amount": amt, "from": c.str("from"), "to": c.str("to"), "rate": math.Round(r*10000) / 10000, "converted": round2(amt * r)}

	// --- expenses ---
	case "log_expense":
		amt, _ := c.num("amount")
		if amt <= 0 {
			return errResult("INVALID_ARGUMENT", false, "amount must be positive")
		}
		extra := map[string]any{}
		if !c.has("date") {
			extra["date"] = c.env.now().Format("2006-01-02")
		}
		return map[string]any{"ok": true, "expense": c.create("expenses", "exp", extra)}
	case "update_expense":
		e, err := c.byID("expenses", "expense_id")
		if err != nil {
			return err
		}
		if amt, ok := c.num("amount"); ok && amt <= 0 {
			return errResult("INVALID_ARGUMENT", false, "amount must be positive")
		}
		c.update(e, "expense_id")
		return map[string]any{"ok": true, "expense": e}
	case "delete_expense":
		return c.deleteBy("expenses", "expense_id")
	case "list_expenses":
		var out []map[string]any
		for _, e := range c.within(c.items("expenses"), "date") {
			if cat := c.str("category"); cat != "" && e["category"] != cat {
				continue
			}
			out = append(out, e)
		}
		sort.SliceStable(out, func(i, j int) bool { return fmt.Sprint(out[i]["date"]) > fmt.Sprint(out[j]["date"]) })
		return c.page("expenses", out, 10)
	case "expense_summary":
		cur := c.str("currency")
		if cur == "" {
			cur = "CNY"
		}
		totals := map[string]float64{}
		total := 0.0
		n := 0
		for _, e := range c.items("expenses") {
			if !strings.HasPrefix(fmt.Sprint(e["date"]), c.str("month")) {
				continue
			}
			amt, _ := toFloat(e["amount"])
			from := fmt.Sprint(e["currency"])
			v := amt * usdPer[cur] / usdPer[from]
			totals[fmt.Sprint(e["category"])] += v
			total += v
			n++
		}
		by := map[string]any{}
		for k, v := range totals {
			by[k] = round2(v)
		}
		return map[string]any{"month": c.str("month"), "currency": cur, "total": round2(total), "by_category": by, "count": n}
	case "set_budget":
		for _, b := range c.items("budgets") {
			if b["category"] == c.str("category") && b["month"] == c.str("month") {
				c.update(b, "")
				return map[string]any{"ok": true, "budget": b}
			}
		}
		return map[string]any{"ok": true, "budget": c.create("budgets", "bud", nil)}
	case "get_budget":
		for _, b := range c.items("budgets") {
			if b["category"] == c.str("category") && b["month"] == c.str("month") {
				amt, _ := toFloat(b["amount"])
				cur := fmt.Sprint(b["currency"])
				if cur == "" || cur == "<nil>" {
					cur = "CNY"
				}
				spent := 0.0
				for _, e := range c.items("expenses") {
					if e["category"] == b["category"] && strings.HasPrefix(fmt.Sprint(e["date"]), c.str("month")) {
						a, _ := toFloat(e["amount"])
						spent += a * usdPer[cur] / usdPer[fmt.Sprint(e["currency"])]
					}
				}
				return map[string]any{"category": b["category"], "month": b["month"], "budget": amt, "currency": cur, "spent": round2(spent), "remaining": round2(amt - spent)}
			}
		}
		return errResult("NOT_FOUND", false, "no %s budget set for %s", c.str("category"), c.str("month"))

	// --- calculation ---
	case "calculator":
		v, err := EvalExpr(c.str("expression"))
		if err != nil {
			return errResult("INVALID_ARGUMENT", false, "%v", err)
		}
		return map[string]any{"result": v}
	case "unit_convert":
		v, _ := c.num("value")
		r, err := convertUnit(v, c.str("from_unit"), c.str("to_unit"))
		if err != nil {
			return errResult("INVALID_ARGUMENT", false, "%v", err)
		}
		return map[string]any{"value": v, "from_unit": c.str("from_unit"), "to_unit": c.str("to_unit"), "result": math.Round(r*10000) / 10000}

	// --- language ---
	case "translate_text":
		if tr, ok := c.st["translations"].(map[string]any); ok {
			if byLang, ok := tr[c.str("text")].(map[string]any); ok {
				if s, ok := byLang[c.str("target_lang")]; ok {
					return map[string]any{"translated_text": s, "target_lang": c.str("target_lang")}
				}
			}
		}
		return map[string]any{"translated_text": "[" + c.str("target_lang") + "] " + c.str("text"), "target_lang": c.str("target_lang")}
	case "detect_language":
		return map[string]any{"language": detectLang(c.str("text"))}

	// --- web ---
	case "web_search", "news_search":
		key := "search"
		if c.name == "news_search" {
			key = "news"
		}
		var out []any
		for _, e := range c.items(key) {
			kws, _ := e["keywords"].([]any)
			hit := false
			for _, k := range kws {
				hit = hit || strings.Contains(strings.ToLower(c.str("query")), strings.ToLower(fmt.Sprint(k)))
			}
			if hit {
				rs, _ := e["results"].([]any)
				out = append(out, rs...)
			}
		}
		if n, ok := c.num("max_results"); ok && int(n) < len(out) {
			out = out[:int(n)]
		}
		return map[string]any{"query": c.str("query"), "results": nonNilAny(out)}
	case "fetch_url":
		pg := c.pageAt(c.str("url"))
		if pg == nil {
			return errResult("NOT_FOUND", false, "HTTP 404 for %s", c.str("url"))
		}
		return map[string]any{"url": c.str("url"), "status": 200, "title": pg["title"], "text": pg["text"]}
	case "browser_open":
		pg := c.pageAt(c.str("url"))
		if pg == nil {
			return errResult("NOT_FOUND", false, "HTTP 404 for %s", c.str("url"))
		}
		tab := c.create("_tabs", "tab", map[string]any{"current": c.str("url")})
		return map[string]any{"tab_id": tab["id"], "url": c.str("url"), "title": pg["title"], "text": pg["text"], "links": linkNames(pg)}
	case "browser_click":
		tab, err := c.byID("_tabs", "tab_id")
		if err != nil {
			return err
		}
		pg := c.pageAt(fmt.Sprint(tab["current"]))
		links, _ := pg["links"].(map[string]any)
		var target string
		for text, url := range links {
			if strings.EqualFold(strings.TrimSpace(text), c.str("link_text")) || strings.Contains(strings.ToLower(text), strings.ToLower(c.str("link_text"))) && c.str("link_text") != "" {
				target = fmt.Sprint(url)
				break
			}
		}
		if target == "" {
			return errResult("NOT_FOUND", false, "no link %q on this page (links: %s)", c.str("link_text"), strings.Join(linkNames(pg), ", "))
		}
		next := c.pageAt(target)
		if next == nil {
			return errResult("NOT_FOUND", false, "HTTP 404 for %s", target)
		}
		tab["current"] = target
		return map[string]any{"tab_id": tab["id"], "url": target, "title": next["title"], "text": next["text"], "links": linkNames(next)}
	case "browser_read":
		tab, err := c.byID("_tabs", "tab_id")
		if err != nil {
			return err
		}
		pg := c.pageAt(fmt.Sprint(tab["current"]))
		return map[string]any{"tab_id": tab["id"], "url": tab["current"], "title": pg["title"], "text": pg["text"], "links": linkNames(pg)}

	// --- maps ---
	case "search_places":
		var out []map[string]any
		for _, p := range c.items("places") {
			if !matchesQuery(p, c.str("query"), "name", "category", "name_zh", "tags") {
				continue
			}
			if open, _ := c.args["open_now"].(bool); open && p["open_now"] == false {
				continue
			}
			out = append(out, p)
		}
		return map[string]any{"places": nonNil(out)}
	case "get_directions", "estimate_travel_time":
		km := 2 + float64(hashOf(strings.ToLower(c.str("origin")+"|"+c.str("destination")))%280)/10
		speed := map[string]float64{"driving": 28, "transit": 22, "walking": 4.8, "cycling": 14}[c.str("mode")]
		mins := math.Round(km / speed * 60)
		if c.name == "estimate_travel_time" {
			if t, ok := c.timeArg("depart_at"); ok && (t.Hour() >= 7 && t.Hour() < 10 || t.Hour() >= 17 && t.Hour() < 20) && c.str("mode") == "driving" {
				mins = math.Round(mins * 1.6)
			}
			return map[string]any{"minutes": mins, "mode": c.str("mode")}
		}
		return map[string]any{"distance_km": km, "duration_min": mins, "mode": c.str("mode"),
			"summary": fmt.Sprintf("%s to %s, %.1f km by %s", c.str("origin"), c.str("destination"), km, c.str("mode"))}
	case "geocode":
		h := hashOf(strings.ToLower(c.str("address")))
		return map[string]any{"address": c.str("address"), "lat": 31.0 + float64(h%1000)/1000, "lng": 121.0 + float64(h/1000%1000)/1000}

	// --- memory ---
	case "memory_save":
		return map[string]any{"ok": true, "memory": c.create("memories", "mem", nil)}
	case "memory_recall":
		var out []map[string]any
		for _, m := range c.items("memories") {
			if looseMatch(m, c.str("query"), "content", "tags") {
				out = append(out, m)
			}
		}
		return map[string]any{"memories": nonNil(out)}
	case "memory_delete":
		return c.deleteBy("memories", "memory_id")

	// --- settings ---
	case "get_settings":
		return map[string]any{"settings": c.st["settings"]}
	case "update_setting":
		s, _ := c.st["settings"].(map[string]any)
		if s == nil {
			s = map[string]any{}
			c.st["settings"] = s
		}
		s[c.str("key")] = c.str("value")
		return map[string]any{"ok": true, "settings": s}
	case "set_do_not_disturb":
		s, _ := c.st["settings"].(map[string]any)
		if s == nil {
			s = map[string]any{}
			c.st["settings"] = s
		}
		s["do_not_disturb"] = c.args["enabled"]
		if c.has("until") {
			s["do_not_disturb_until"] = c.str("until")
		}
		return map[string]any{"ok": true, "do_not_disturb": s["do_not_disturb"], "until": s["do_not_disturb_until"]}

	// --- health ---
	case "log_health_metric":
		if c.str("metric") == "blood_pressure" && !c.has("value2") {
			return errResult("INVALID_ARGUMENT", false, "blood_pressure needs value (systolic) and value2 (diastolic)")
		}
		extra := map[string]any{}
		if !c.has("time") {
			extra["time"] = c.env.now().Format(time.RFC3339)
		}
		return map[string]any{"ok": true, "entry": c.create("health_log", "hl", extra)}
	case "get_health_metrics":
		var out []map[string]any
		for _, h := range c.within(c.items("health_log"), "time") {
			if h["metric"] == c.str("metric") {
				out = append(out, h)
			}
		}
		sortByTime(out, "time")
		return map[string]any{"metric": c.str("metric"), "entries": nonNil(out)}
	case "log_medication":
		extra := map[string]any{}
		if !c.has("taken_at") {
			extra["taken_at"] = c.env.now().Format(time.RFC3339)
		}
		return map[string]any{"ok": true, "entry": c.create("medications", "med", extra)}
	case "log_workout":
		return map[string]any{"ok": true, "workout": c.create("workouts", "wk", nil)}

	// --- shopping ---
	case "add_to_shopping_list":
		return map[string]any{"ok": true, "item": c.create("shopping", "shop", nil)}
	case "list_shopping_list":
		return map[string]any{"items": nonNil(c.items("shopping"))}
	case "remove_from_shopping_list":
		return c.deleteBy("shopping", "item_id")

	// --- travel ---
	case "search_flights":
		if fl := c.items("flights"); len(fl) > 0 {
			return map[string]any{"flights": fl}
		}
		h := hashOf(strings.ToLower(c.str("origin") + c.str("destination") + c.str("date")))
		var out []any
		for i := 0; i < 3; i++ {
			dep := 7 + int(h>>(i*3)%12)
			out = append(out, map[string]any{"flight_number": fmt.Sprintf("MU%d", 5100+int(h%90)+i*7), "depart": fmt.Sprintf("%sT%02d:%02d:00+08:00", c.str("date"), dep, 5*int(h>>i%12)),
				"duration_min": 95 + int(h%60), "price": 680 + int(h>>(i*4)%900), "currency": "CNY"})
		}
		return map[string]any{"flights": out}
	case "get_flight_status":
		for _, f := range c.items("flight_status") {
			if strings.EqualFold(fmt.Sprint(f["flight_number"]), c.str("flight_number")) {
				return f
			}
		}
		return map[string]any{"flight_number": strings.ToUpper(c.str("flight_number")), "date": c.str("date"), "status": "scheduled", "delay_min": 0}
	case "search_hotels":
		if hs := c.items("hotels"); len(hs) > 0 {
			return map[string]any{"hotels": hs}
		}
		h := hashOf(strings.ToLower(c.str("city") + c.str("check_in")))
		var out []any
		for i, n := range []string{"Riverside Inn", "Central Plaza Hotel", "Garden Courtyard"} {
			out = append(out, map[string]any{"name": n, "price_per_night": 420 + int(h>>(i*5)%700), "currency": "CNY", "rating": 7.5 + float64(h>>(i*2)%25)/10})
		}
		return map[string]any{"hotels": out}

	// --- smart home ---
	case "list_devices":
		var out []map[string]any
		for _, d := range c.items("devices") {
			if r := c.str("room"); r == "" || strings.EqualFold(fmt.Sprint(d["room"]), r) {
				out = append(out, d)
			}
		}
		return map[string]any{"devices": nonNil(out)}
	case "control_device":
		d, err := c.byID("devices", "device_id")
		if err != nil {
			return err
		}
		switch c.str("action") {
		case "turn_on":
			d["state"] = "on"
		case "turn_off":
			d["state"] = "off"
		default:
			d["state"] = map[bool]string{true: "off", false: "on"}[d["state"] == "on"]
		}
		return map[string]any{"ok": true, "device": d}
	case "set_thermostat":
		d, err := c.byID("devices", "device_id")
		if err != nil {
			return err
		}
		if d["type"] != "ac" {
			return errResult("INVALID_ARGUMENT", false, "%s is a %s, not a thermostat", d["id"], d["type"])
		}
		t, _ := c.num("target_c")
		if t < 16 || t > 30 {
			return errResult("INVALID_ARGUMENT", false, "target_c must be between 16 and 30")
		}
		d["target_c"] = t
		d["state"] = "on"
		if m := c.str("mode"); m != "" {
			d["mode"] = m
		}
		return map[string]any{"ok": true, "device": d}

	// --- misc ---
	case "track_package":
		for _, p := range c.items("packages") {
			if strings.EqualFold(fmt.Sprint(p["tracking_number"]), c.str("tracking_number")) {
				return p
			}
		}
		return errResult("NOT_FOUND", false, "no shipment found for %s", c.str("tracking_number"))
	case "get_user_profile":
		return map[string]any{"profile": defaultUser}
	}
	return errResult("UNIMPLEMENTED", false, "%s is not implemented", c.name)
}

func (c *call) deleteBy(coll, idArg string) any {
	if _, err := c.byID(coll, idArg); err != nil {
		return err
	}
	c.remove(coll, c.str(idArg))
	return map[string]any{"ok": true, "deleted": c.str(idArg)}
}

// fromMatches lets a sender filter name a person the way a mail client does:
// "Kenji" or "周浩" finds mail from that contact's address.
func (c *call) fromMatches(addr, filter string) bool {
	addr, f := strings.ToLower(addr), strings.ToLower(strings.TrimSpace(filter))
	if strings.Contains(addr, f) {
		return true
	}
	for _, ct := range c.items("contacts") {
		email := strings.ToLower(fmt.Sprint(ct["email"]))
		if email != "" && email == addr && matchesQuery(ct, f, "name", "name_zh", "relationship") {
			return true
		}
	}
	return false
}

func (c *call) file(path string) map[string]any {
	for _, f := range c.items("files") {
		if f["path"] == path {
			return f
		}
	}
	return nil
}

func (c *call) pageAt(url string) map[string]any {
	pages, _ := c.st["pages"].(map[string]any)
	pg, _ := pages[strings.TrimRight(url, "/")].(map[string]any)
	if pg == nil {
		pg, _ = pages[url].(map[string]any)
	}
	return pg
}

func linkNames(pg map[string]any) []string {
	links, _ := pg["links"].(map[string]any)
	var out []string
	for k := range links {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (c *call) overlapping(start, end time.Time, self any) []map[string]any {
	var out []map[string]any
	for _, ev := range c.items("events") {
		if ev["id"] == self {
			continue
		}
		s, ok1 := itemTime(ev, "start")
		e, ok2 := itemTime(ev, "end")
		if ok1 && ok2 && s.Before(end) && start.Before(e) {
			out = append(out, brief(ev, "id", "title", "start", "end"))
		}
	}
	return out
}

func (c *call) freeSlots() any {
	day, err := time.ParseInLocation("2006-01-02", c.str("date"), shanghai)
	if err != nil {
		return errResult("INVALID_ARGUMENT", false, "date must be YYYY-MM-DD")
	}
	at := func(k, def string) time.Time {
		s := c.str(k)
		if s == "" {
			s = def
		}
		hm, _ := time.Parse("15:04", s)
		return day.Add(time.Duration(hm.Hour())*time.Hour + time.Duration(hm.Minute())*time.Minute)
	}
	lo, hi := at("earliest", "09:00"), at("latest", "18:00")
	need, _ := c.num("duration_min")
	type span struct{ s, e time.Time }
	var busy []span
	for _, ev := range c.items("events") {
		s, ok1 := itemTime(ev, "start")
		e, ok2 := itemTime(ev, "end")
		if ok1 && ok2 && s.Before(hi) && lo.Before(e) {
			busy = append(busy, span{s, e})
		}
	}
	sort.Slice(busy, func(i, j int) bool { return busy[i].s.Before(busy[j].s) })
	var free []any
	cur := lo
	for _, b := range append(busy, span{hi, hi}) {
		if b.s.Sub(cur).Minutes() >= need {
			free = append(free, map[string]any{"start": cur.Format(time.RFC3339), "end": b.s.Format(time.RFC3339)})
		}
		if b.e.After(cur) {
			cur = b.e
		}
	}
	return map[string]any{"date": c.str("date"), "free": nonNilAny(free)}
}

var rePhone = regexp.MustCompile(`^\+\d[\d\s\-]{6,18}\d$`)

func badEmails(v any) string {
	var list []string
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		list = []string{x}
	case []any:
		for _, e := range x {
			list = append(list, fmt.Sprint(e))
		}
	}
	for _, e := range list {
		if a, err := mail.ParseAddress(e); err != nil || !strings.Contains(a.Address, ".") {
			return e
		}
	}
	return ""
}

func brief(m map[string]any, keys ...string) map[string]any {
	out := map[string]any{}
	for _, k := range keys {
		if v, ok := m[k]; ok {
			out[k] = v
		}
	}
	if _, ok := out["snippet"]; !ok {
		if b, ok := m["body"].(string); ok {
			r := []rune(b)
			out["snippet"] = string(r[:min(len(r), 80)])
		}
	}
	return out
}

func reverse(s []map[string]any) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

func nonNil(s []map[string]any) []map[string]any {
	if s == nil {
		return []map[string]any{}
	}
	return s
}

func nonNilAny(s []any) []any {
	if s == nil {
		return []any{}
	}
	return s
}

func detectLang(s string) string {
	for _, r := range s {
		switch {
		case unicode.In(r, unicode.Hiragana, unicode.Katakana):
			return "ja"
		case unicode.Is(unicode.Hangul, r):
			return "ko"
		}
	}
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return "zh"
		}
	}
	return "en"
}

// --- data ---

var usdPer = map[string]float64{
	"USD": 1, "CNY": 7.12, "EUR": 0.92, "GBP": 0.79, "JPY": 152.0, "HKD": 7.78,
	"KRW": 1385, "SGD": 1.34, "AUD": 1.52, "THB": 34.5, "CHF": 0.86,
}

type quote struct {
	name, cur  string
	price, chg float64
}

var stocks = map[string]quote{
	"AAPL": {"Apple Inc.", "USD", 231.40, 0.8}, "MSFT": {"Microsoft", "USD", 428.15, -0.3},
	"NVDA": {"NVIDIA", "USD", 138.20, 2.1}, "TSLA": {"Tesla", "USD", 251.30, -1.7},
	"BABA": {"Alibaba ADR", "USD", 98.70, 1.2}, "0700.HK": {"Tencent", "HKD", 412.60, 0.5},
	"9988.HK": {"Alibaba", "HKD", 96.10, 1.1}, "600519.SS": {"Kweichow Moutai", "CNY", 1532.00, -0.4},
	"300750.SZ": {"CATL", "CNY", 241.80, 0.9}, "VOO": {"Vanguard S&P 500 ETF", "USD", 512.30, 0.4},
}

var crypto = map[string]float64{"BTC": 68250, "ETH": 2480, "SOL": 152.4}

var holidays2026 = map[string][]map[string]string{
	"CN": {{"date": "2026-01-01", "name": "New Year's Day 元旦"}, {"date": "2026-02-17", "name": "Spring Festival 春节"},
		{"date": "2026-04-05", "name": "Qingming 清明节"}, {"date": "2026-05-01", "name": "Labour Day 劳动节"},
		{"date": "2026-06-19", "name": "Dragon Boat Festival 端午节"}, {"date": "2026-09-25", "name": "Mid-Autumn Festival 中秋节"},
		{"date": "2026-10-01", "name": "National Day 国庆节"}},
	"US": {{"date": "2026-01-01", "name": "New Year's Day"}, {"date": "2026-07-04", "name": "Independence Day"},
		{"date": "2026-11-26", "name": "Thanksgiving"}, {"date": "2026-12-25", "name": "Christmas Day"}},
	"JP": {{"date": "2026-01-01", "name": "New Year's Day"}, {"date": "2026-11-03", "name": "Culture Day"}, {"date": "2026-11-23", "name": "Labour Thanksgiving Day"}},
	"GB": {{"date": "2026-01-01", "name": "New Year's Day"}, {"date": "2026-12-25", "name": "Christmas Day"}, {"date": "2026-12-26", "name": "Boxing Day"}},
}

var knownWeather = map[string][2]any{
	"shanghai": {"cloudy", 19}, "上海": {"cloudy", 19}, "beijing": {"sunny", 12}, "北京": {"sunny", 12},
	"hangzhou": {"light rain", 17}, "杭州": {"light rain", 17}, "shenzhen": {"sunny", 27}, "深圳": {"sunny", 27},
	"chengdu": {"overcast", 16}, "成都": {"overcast", 16}, "tokyo": {"sunny", 18}, "东京": {"sunny", 18},
	"london": {"rain", 11}, "paris": {"cloudy", 13}, "new york": {"sunny", 15}, "chicago": {"windy", 9},
}

func weatherFor(city string, day int) map[string]any {
	key := strings.ToLower(strings.TrimSpace(city))
	conds := []string{"sunny", "cloudy", "light rain", "overcast", "heavy rain", "thunderstorms"}
	h := hashOf(fmt.Sprintf("%s|%d", key, day))
	cond, temp := conds[h%uint32(len(conds))], 8+int(h/7%22)
	if kw, ok := knownWeather[key]; ok && day == 0 {
		cond, temp = kw[0].(string), kw[1].(int)
	}
	rain := map[string]int{"sunny": 0, "cloudy": 10, "overcast": 20, "light rain": 70, "heavy rain": 95, "thunderstorms": 90, "rain": 80, "windy": 10}[cond]
	return map[string]any{"city": city, "condition": cond, "temp_c": temp, "high_c": temp + 4, "low_c": temp - 5, "precip_chance_pct": rain}
}

func imperial(c *call, w map[string]any) map[string]any {
	if c.str("units") != "imperial" {
		return w
	}
	for _, k := range []string{"temp_c", "high_c", "low_c"} {
		if v, ok := w[k].(int); ok {
			w[strings.TrimSuffix(k, "_c")+"_f"] = math.Round(float64(v)*9/5 + 32)
			delete(w, k)
		}
	}
	return w
}

func aqiLevel(a int) string {
	switch {
	case a <= 50:
		return "good"
	case a <= 100:
		return "moderate"
	default:
		return "unhealthy for sensitive groups"
	}
}

// unitTable maps a unit to (dimension, factor to the SI base).
var unitTable = map[string]struct {
	dim string
	f   float64
}{
	"m": {"length", 1}, "km": {"length", 1000}, "cm": {"length", 0.01}, "mm": {"length", 0.001},
	"mi": {"length", 1609.344}, "ft": {"length", 0.3048}, "in": {"length", 0.0254}, "yd": {"length", 0.9144}, "li": {"length", 500},
	"kg": {"mass", 1}, "g": {"mass", 0.001}, "lb": {"mass", 0.45359237}, "oz": {"mass", 0.028349523125}, "jin": {"mass", 0.5}, "liang": {"mass", 0.05}, "t": {"mass", 1000},
	"l": {"volume", 1}, "ml": {"volume", 0.001}, "gal": {"volume", 3.785411784}, "cup": {"volume", 0.2365882365}, "floz": {"volume", 0.0295735295625},
	"m2": {"area", 1}, "km2": {"area", 1e6}, "ha": {"area", 1e4}, "acre": {"area", 4046.8564224}, "mu": {"area", 666.6667}, "sqft": {"area", 0.09290304},
	"kmh": {"speed", 1 / 3.6}, "mph": {"speed", 0.44704}, "ms": {"speed", 1}, "knot": {"speed", 0.514444},
}

var unitAlias = map[string]string{
	"meter": "m", "meters": "m", "metre": "m", "kilometer": "km", "kilometers": "km", "mile": "mi", "miles": "mi", "feet": "ft", "foot": "ft",
	"inch": "in", "inches": "in", "kilogram": "kg", "kilograms": "kg", "gram": "g", "grams": "g", "pound": "lb", "pounds": "lb", "lbs": "lb",
	"ounce": "oz", "ounces": "oz", "斤": "jin", "两": "liang", "里": "li", "亩": "mu", "公里": "km", "千米": "km", "米": "m", "公斤": "kg", "千克": "kg", "克": "g",
	"liter": "l", "liters": "l", "litre": "l", "升": "l", "毫升": "ml", "gallon": "gal", "gallons": "gal", "cups": "cup", "fl oz": "floz",
	"sq m": "m2", "square meter": "m2", "平方米": "m2", "hectare": "ha", "acres": "acre", "km/h": "kmh", "kph": "kmh", "m/s": "ms",
	"celsius": "c", "°c": "c", "摄氏度": "c", "fahrenheit": "f", "°f": "f", "华氏度": "f", "kelvin": "k",
}

func normUnit(u string) string {
	u = strings.ToLower(strings.TrimSpace(u))
	if a, ok := unitAlias[u]; ok {
		return a
	}
	return u
}

func convertUnit(v float64, from, to string) (float64, error) {
	f, t := normUnit(from), normUnit(to)
	temp := map[string]bool{"c": true, "f": true, "k": true}
	if temp[f] || temp[t] {
		if !temp[f] || !temp[t] {
			return 0, fmt.Errorf("cannot convert %s to %s", from, to)
		}
		c := v
		switch f {
		case "f":
			c = (v - 32) * 5 / 9
		case "k":
			c = v - 273.15
		}
		switch t {
		case "f":
			return c*9/5 + 32, nil
		case "k":
			return c + 273.15, nil
		}
		return c, nil
	}
	a, ok1 := unitTable[f]
	b, ok2 := unitTable[t]
	if !ok1 || !ok2 {
		return 0, fmt.Errorf("unsupported unit %q or %q", from, to)
	}
	if a.dim != b.dim {
		return 0, fmt.Errorf("cannot convert %s (%s) to %s (%s)", from, a.dim, to, b.dim)
	}
	return v * a.f / b.f, nil
}

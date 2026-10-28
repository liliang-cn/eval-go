package tools

// The "life" profile: a personal-assistant tool surface of the size and shape a
// real assistant mounts on every request — notes, to-dos, calendar,
// reminders, alarms, timers, contacts, mail, messaging, files, markets,
// expenses, health, maps, travel, smart home, memory, settings, web.
//
// It is deliberately crowded with near-duplicates, because choosing among
// them is the skill under test: create_reminder / create_alarm / create_todo /
// start_timer / create_event; send_email / draft_email / reply_email;
// send_sms / send_chat_message; get_fx_rate / convert_currency; get_weather /
// get_weather_forecast; web_search / news_search / fetch_url / browser_open;
// memory_save / create_note; get_directions / estimate_travel_time;
// control_device / set_thermostat.
//
// Schemas are realistic: enums, formats, required fields, arrays, nested
// objects. Arguments are validated the way an API would, so a wrong format
// comes back as an INVALID_ARGUMENT the agent has to read and fix.

func ls(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
func ln(d string) map[string]any { return map[string]any{"type": "number", "description": d} }
func li(d string, lo ...float64) map[string]any {
	m := map[string]any{"type": "integer", "description": d}
	if len(lo) > 0 {
		m["minimum"] = lo[0]
	}
	if len(lo) > 1 {
		m["maximum"] = lo[1]
	}
	return m
}
func lb(d string) map[string]any { return map[string]any{"type": "boolean", "description": d} }
func le(d string, vals ...string) map[string]any {
	return map[string]any{"type": "string", "enum": vals, "description": d}
}
func ldt(d string) map[string]any {
	return map[string]any{"type": "string", "format": "date-time", "description": d + " (ISO 8601 with offset, e.g. 2026-10-29T19:00:00+08:00)"}
}
func ldate(d string) map[string]any {
	return map[string]any{"type": "string", "format": "date", "description": d + " (YYYY-MM-DD)"}
}
func lhm(d string) map[string]any {
	return map[string]any{"type": "string", "format": "time", "description": d + " (24h HH:MM)"}
}
func la(d string, items map[string]any) map[string]any {
	return map[string]any{"type": "array", "description": d, "items": items}
}
func lo(d string, props map[string]any, req ...string) map[string]any {
	s := schema(props, req...)
	s["description"] = d
	return s
}

type P = map[string]any

var (
	currencies = []string{"CNY", "USD", "EUR", "GBP", "JPY", "HKD", "KRW", "SGD", "AUD", "THB", "CHF"}
	langs      = []string{"en", "zh", "ja", "ko", "fr", "de", "es", "it", "ru", "pt"}
	weekdays   = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}
	expenseCat = []string{"food", "groceries", "transport", "housing", "utilities", "entertainment", "health", "shopping", "travel", "education", "other"}
)

func lifeTools() []Tool {
	t := func(name, desc string, props P, req ...string) Tool {
		return Tool{name, desc, schema(props, req...)}
	}
	page := func(p P) P {
		p["page_size"] = li("Results per page (default 10, max 50)", 1, 50)
		p["page_token"] = ls("Token from a previous response's next_page_token")
		return p
	}
	return []Tool{
		// --- time ---
		t("get_current_time", "Get the current date, time and weekday, optionally in another IANA time zone.",
			P{"timezone": ls("IANA zone, e.g. Europe/Paris; defaults to the user's zone")}),
		t("convert_timezone", "Convert a date-time from one IANA time zone to another.",
			P{"time": ls("Date-time to convert, e.g. 2026-10-30T15:00"), "from_tz": ls("IANA zone of the given time"), "to_tz": ls("IANA zone to convert to")},
			"time", "from_tz", "to_tz"),
		t("date_calc", "Date arithmetic: add days to a date, count days between two dates, or get a date's weekday.",
			P{"operation": le("What to compute", "add_days", "days_between", "weekday"), "date": ldate("Base date"), "days": li("Days to add (may be negative)"), "date2": ldate("Second date for days_between")},
			"operation", "date"),
		t("get_holidays", "List public holidays for a country and year.",
			P{"country": le("Country code", "CN", "US", "JP", "GB"), "year": li("Year", 2020, 2030)}, "country", "year"),

		// --- weather ---
		t("get_weather", "Get CURRENT weather conditions for a city (right now only, no forecast).",
			P{"city": ls("City name"), "units": le("Temperature units", "metric", "imperial")}, "city"),
		t("get_weather_forecast", "Get the daily weather FORECAST for a city for the next 1-7 days (day 1 = today).",
			P{"city": ls("City name"), "days": li("Number of days, 1-7", 1, 7), "units": le("Temperature units", "metric", "imperial")}, "city", "days"),
		t("get_air_quality", "Get the current air quality index (AQI) and main pollutant for a city.",
			P{"city": ls("City name")}, "city"),

		// --- notes ---
		t("create_note", "Create a note in the notes app (free text the user wants to keep and read later).",
			P{"title": ls("Note title"), "content": ls("Note body"), "tags": la("Tags", ls("tag")), "folder": ls("Folder name, default 'Notes'")}, "title", "content"),
		t("list_notes", "List notes, optionally filtered by folder or tag, newest first.",
			page(P{"folder": ls("Folder name"), "tag": ls("Tag")})),
		t("search_notes", "Full-text search over notes (title and body).",
			P{"query": ls("Words to search for")}, "query"),
		t("get_note", "Read one note in full by id.", P{"note_id": ls("Note id")}, "note_id"),
		t("update_note", "Replace a note's title and/or content.",
			P{"note_id": ls("Note id"), "title": ls("New title"), "content": ls("New full content (replaces the old body)")}, "note_id"),
		t("append_to_note", "Append text to the end of an existing note without replacing it.",
			P{"note_id": ls("Note id"), "text": ls("Text to append")}, "note_id", "text"),
		t("delete_note", "Delete a note permanently.", P{"note_id": ls("Note id")}, "note_id"),

		// --- to-dos ---
		t("create_todo", "Add a task to the to-do list, optionally with a due date. A to-do does NOT notify the user; use create_reminder for a notification.",
			P{"title": ls("What to do"), "due": ldt("Due date-time"), "priority": le("Priority", "low", "normal", "high"), "list": ls("List name, default 'Inbox'")}, "title"),
		t("list_todos", "List to-dos.",
			P{"status": le("Which to list (default open)", "open", "done", "all"), "list": ls("List name")}),
		t("complete_todo", "Mark a to-do as done.", P{"todo_id": ls("To-do id")}, "todo_id"),
		t("update_todo", "Change a to-do's title, due date or priority.",
			P{"todo_id": ls("To-do id"), "title": ls("New title"), "due": ldt("New due date-time"), "priority": le("Priority", "low", "normal", "high")}, "todo_id"),
		t("delete_todo", "Delete a to-do.", P{"todo_id": ls("To-do id")}, "todo_id"),

		// --- calendar ---
		t("create_event", "Add an event with a start and end time to the user's calendar. Attendees get an invitation email.",
			P{"title": ls("Event title"), "start": ldt("Start"), "end": ldt("End"), "location": ls("Place"),
				"attendees": la("Attendee email addresses", ls("email")), "notes": ls("Description"),
				"reminder_minutes": li("Notify this many minutes before start", 0, 10080)},
			"title", "start", "end"),
		t("list_events", "List calendar events in a time range, optionally matching a query.",
			P{"from": ldt("Range start"), "to": ldt("Range end"), "query": ls("Text to match in title/location")}),
		t("update_event", "Change an existing calendar event (time, title, place, attendees).",
			P{"event_id": ls("Event id"), "title": ls("New title"), "start": ldt("New start"), "end": ldt("New end"), "location": ls("New place"),
				"attendees": la("Full new attendee list", ls("email"))}, "event_id"),
		t("delete_event", "Delete (cancel) a calendar event. Attendees are notified.", P{"event_id": ls("Event id")}, "event_id"),
		t("find_free_slots", "Find free time on one day of the user's calendar.",
			P{"date": ldate("Day to search"), "duration_min": li("Length of slot needed", 5, 720), "earliest": lhm("Earliest start"), "latest": lhm("Latest end")},
			"date", "duration_min"),

		// --- reminders ---
		t("create_reminder", "Create a reminder that NOTIFIES the user at a specific date and time ('remind me to ...'). One-off or repeating.",
			P{"title": ls("What to remind about"), "remind_at": ldt("When to notify"), "repeat": le("Repeat rule (default none)", "none", "daily", "weekdays", "weekly", "monthly"), "note": ls("Extra detail")},
			"title", "remind_at"),
		t("list_reminders", "List reminders.",
			P{"status": le("Which (default pending)", "pending", "done", "all"), "from": ldt("Range start"), "to": ldt("Range end")}),
		t("update_reminder", "Change a reminder's text, time or repeat rule.",
			P{"reminder_id": ls("Reminder id"), "title": ls("New text"), "remind_at": ldt("New time"), "repeat": le("Repeat rule", "none", "daily", "weekdays", "weekly", "monthly")}, "reminder_id"),
		t("delete_reminder", "Delete a reminder.", P{"reminder_id": ls("Reminder id")}, "reminder_id"),
		t("snooze_reminder", "Push a reminder back by some minutes.", P{"reminder_id": ls("Reminder id"), "minutes": li("Minutes to snooze", 1, 1440)}, "reminder_id", "minutes"),

		// --- alarms ---
		t("create_alarm", "Set an ALARM that rings on the phone like an alarm clock at a time of day (wake-up, 'ring at 6:30'). Optionally repeating on weekdays.",
			P{"time": lhm("Time of day"), "label": ls("Label"), "repeat_days": la("Days it repeats on; empty = one-off", le("day", weekdays...)),
				"date": ldate("For a one-off alarm on a specific date (default: next occurrence)"), "skip_holidays": lb("Do not ring on public holidays (CN calendar)")},
			"time"),
		t("list_alarms", "List alarms.", P{}),
		t("update_alarm", "Change an alarm (time, label, days, enabled).",
			P{"alarm_id": ls("Alarm id"), "time": lhm("New time"), "label": ls("New label"), "enabled": lb("On or off"), "repeat_days": la("New repeat days", le("day", weekdays...))}, "alarm_id"),
		t("delete_alarm", "Delete an alarm.", P{"alarm_id": ls("Alarm id")}, "alarm_id"),

		// --- timers ---
		t("start_timer", "Start a countdown timer for a duration (e.g. 'a 10 minute timer'). Not for a time of day.",
			P{"duration_seconds": li("Duration in seconds", 1, 86400), "label": ls("Label")}, "duration_seconds"),
		t("list_timers", "List running timers.", P{}),
		t("cancel_timer", "Cancel a running timer.", P{"timer_id": ls("Timer id")}, "timer_id"),

		// --- contacts ---
		t("search_contacts", "Search the address book by name, nickname or relationship (e.g. 'mom', 'dentist'). Returns phone, email and chat handles.",
			P{"query": ls("Name or relationship")}, "query"),
		t("get_contact", "Get one contact by id.", P{"contact_id": ls("Contact id")}, "contact_id"),
		t("create_contact", "Add a contact to the address book.",
			P{"name": ls("Full name"), "phone": ls("Phone in international format, e.g. +86 138 0000 0000"), "email": ls("Email"), "relationship": ls("e.g. friend, plumber"), "wechat": ls("WeChat id")}, "name"),
		t("update_contact", "Change a contact's details.",
			P{"contact_id": ls("Contact id"), "phone": ls("New phone"), "email": ls("New email"), "wechat": ls("New WeChat id"), "relationship": ls("New relationship")}, "contact_id"),

		// --- email ---
		t("send_email", "Send an email immediately from the user's account.",
			P{"to": la("Recipient addresses", ls("email")), "subject": ls("Subject"), "body": ls("Body text"), "cc": la("CC addresses", ls("email"))},
			"to", "subject", "body"),
		t("draft_email", "Save an email as a DRAFT for the user to review; nothing is sent.",
			P{"to": la("Recipient addresses", ls("email")), "subject": ls("Subject"), "body": ls("Body text")}, "to", "body"),
		t("search_emails", "Search the user's inbox.",
			page(P{"query": ls("Words to match in subject/body"), "from": ls("Sender address or name"), "unread_only": lb("Only unread")})),
		t("read_email", "Read one email in full.", P{"email_id": ls("Email id")}, "email_id"),
		t("reply_email", "Reply to an email in the inbox (sent immediately, to the original sender).",
			P{"email_id": ls("Email id being replied to"), "body": ls("Reply text")}, "email_id", "body"),

		// --- messaging ---
		t("send_sms", "Send a text message (SMS) to a phone number.",
			P{"phone": ls("Phone number in international format"), "text": ls("Message")}, "phone", "text"),
		t("send_chat_message", "Send a message on a chat app (WeChat, WhatsApp, Telegram) to a contact's handle.",
			P{"app": le("Chat app", "wechat", "whatsapp", "telegram"), "to": ls("Handle / id on that app"), "text": ls("Message")}, "app", "to", "text"),
		t("read_messages", "Read recent incoming chat messages, optionally from one sender.",
			P{"app": le("Chat app", "wechat", "whatsapp", "telegram", "sms"), "from": ls("Sender handle or name"), "limit": li("Max messages", 1, 50)}),

		// --- files ---
		t("list_files", "List files in a folder of the user's cloud drive.", P{"folder": ls("Folder path, e.g. /Documents")}),
		t("read_file", "Read a text file from the user's drive.", P{"path": ls("File path")}, "path"),
		t("write_file", "Create or overwrite a text file on the user's drive.",
			P{"path": ls("File path"), "content": ls("Full content"), "overwrite": lb("Replace an existing file (default false)")}, "path", "content"),
		t("search_files", "Search file names and contents on the user's drive.", P{"query": ls("Words")}, "query"),
		t("share_file", "Share a file with someone by email.",
			P{"path": ls("File path"), "email": ls("Who to share with"), "permission": le("Access level", "view", "edit")}, "path", "email", "permission"),
		t("delete_file", "Delete a file from the drive.", P{"path": ls("File path")}, "path"),

		// --- markets & money ---
		t("get_stock_quote", "Get the latest price for a STOCK or ETF ticker (e.g. AAPL, 0700.HK, 600519.SS).",
			P{"symbol": ls("Ticker symbol")}, "symbol"),
		t("get_crypto_price", "Get the latest price of a CRYPTOCURRENCY (e.g. BTC, ETH).",
			P{"coin": ls("Coin symbol"), "vs_currency": le("Quote currency", "USD", "CNY", "EUR")}, "coin"),
		t("get_fx_rate", "Get the exchange RATE between two currencies (1 base = rate quote). Does not convert an amount.",
			P{"base": le("Base currency", currencies...), "quote": le("Quote currency", currencies...)}, "base", "quote"),
		t("convert_currency", "Convert a MONEY AMOUNT from one currency to another at the current rate.",
			P{"amount": ln("Amount"), "from": le("From currency", currencies...), "to": le("To currency", currencies...)}, "amount", "from", "to"),

		// --- expenses & budgets ---
		t("log_expense", "Record an expense in the user's ledger (记账).",
			P{"amount": ln("Amount spent (positive)"), "currency": le("Currency", currencies...), "category": le("Category", expenseCat...), "note": ls("What it was"), "date": ldate("Date spent (default today)")},
			"amount", "currency", "category"),
		t("list_expenses", "List recorded expenses in a date range, newest first. Paginated.",
			page(P{"from": ldate("From date"), "to": ldate("To date"), "category": le("Category", expenseCat...)})),
		t("expense_summary", "Total spending per category for a month, in one currency.",
			P{"month": ls("Month as YYYY-MM"), "currency": le("Report currency (default CNY)", currencies...)}, "month"),
		t("set_budget", "Set a monthly budget for a category.",
			P{"category": le("Category", expenseCat...), "month": ls("YYYY-MM"), "amount": ln("Budget amount"), "currency": le("Currency", currencies...)}, "category", "month", "amount"),
		t("get_budget", "Get a category's monthly budget, spent so far and remaining.",
			P{"category": le("Category", expenseCat...), "month": ls("YYYY-MM")}, "category", "month"),

		// --- calculation ---
		t("calculator", "Evaluate an arithmetic expression (+ - * / % and parentheses).", P{"expression": ls("Expression")}, "expression"),
		t("unit_convert", "Convert PHYSICAL units: length, mass, volume, area, speed, temperature. Supports Chinese units 斤 (jin), 两 (liang), 里 (li), 亩 (mu).",
			P{"value": ln("Value"), "from_unit": ls("Unit, e.g. km, mi, kg, lb, jin, C, F, L, gal, mu, m2"), "to_unit": ls("Target unit")}, "value", "from_unit", "to_unit"),

		// --- language ---
		t("translate_text", "Translate text into another language.",
			P{"text": ls("Text"), "target_lang": le("Target language", langs...), "source_lang": le("Source language (auto if omitted)", langs...)}, "text", "target_lang"),
		t("detect_language", "Detect the language of a text.", P{"text": ls("Text")}, "text"),

		// --- web ---
		t("web_search", "Search the web. Returns titles, URLs, snippets and publish dates.",
			P{"query": ls("Query"), "max_results": li("1-10", 1, 10), "recency": le("Only results newer than", "any", "day", "week", "month")}, "query"),
		t("news_search", "Search recent NEWS articles.", P{"query": ls("Query"), "since": ldate("Only articles on or after this date")}, "query"),
		t("fetch_url", "Fetch a web page and return its readable text.", P{"url": ls("Absolute URL")}, "url"),
		t("browser_open", "Open a URL in the assistant's browser; returns a tab id, the page text and its links.", P{"url": ls("Absolute URL")}, "url"),
		t("browser_click", "Click a link (by its visible text) in an open browser tab.", P{"tab_id": ls("Tab id from browser_open"), "link_text": ls("Visible link text")}, "tab_id", "link_text"),
		t("browser_read", "Read the current page text of an open browser tab.", P{"tab_id": ls("Tab id")}, "tab_id"),

		// --- maps ---
		t("search_places", "Find places (restaurants, shops, pharmacies...) near a location.",
			P{"query": ls("What to look for"), "near": ls("Address or area; default the user's home"), "open_now": lb("Only places open now")}, "query"),
		t("get_directions", "Get step-by-step directions between two places, with distance and duration.",
			P{"origin": ls("Start"), "destination": ls("End"), "mode": le("Travel mode", "driving", "transit", "walking", "cycling")}, "origin", "destination", "mode"),
		t("estimate_travel_time", "Estimate only the travel time between two places for a departure time, considering typical traffic.",
			P{"origin": ls("Start"), "destination": ls("End"), "mode": le("Travel mode", "driving", "transit", "walking", "cycling"), "depart_at": ldt("Departure time")}, "origin", "destination", "mode"),
		t("geocode", "Turn an address into coordinates.", P{"address": ls("Address")}, "address"),

		// --- memory ---
		t("memory_save", "Save a durable fact or preference about the user to long-term memory (not a note; recalled automatically in future conversations).",
			P{"content": ls("The fact, in one sentence"), "tags": la("Tags", ls("tag"))}, "content"),
		t("memory_recall", "Search long-term memory for facts about the user.", P{"query": ls("What to look for")}, "query"),
		t("memory_delete", "Forget a memory by id.", P{"memory_id": ls("Memory id")}, "memory_id"),

		// --- settings ---
		t("get_settings", "Read the assistant's settings.", P{}),
		t("update_setting", "Change one assistant setting.",
			P{"key": le("Setting", "language", "units", "default_reminder_minutes", "morning_briefing_time", "voice_replies", "currency"), "value": ls("New value")}, "key", "value"),
		t("set_do_not_disturb", "Turn Do Not Disturb on or off, optionally until a time.",
			P{"enabled": lb("On or off"), "until": ldt("Turn off automatically at")}, "enabled"),

		// --- health ---
		t("log_health_metric", "Record a health measurement.",
			P{"metric": le("What was measured", "weight_kg", "blood_pressure", "heart_rate_bpm", "steps", "sleep_hours", "water_ml", "blood_glucose_mmol"),
				"value": ln("Value (systolic for blood_pressure)"), "value2": ln("Diastolic, for blood_pressure only"), "time": ldt("When measured (default now)"), "note": ls("Note")},
			"metric", "value"),
		t("get_health_metrics", "Read recorded health measurements of one kind in a range.",
			P{"metric": le("Metric", "weight_kg", "blood_pressure", "heart_rate_bpm", "steps", "sleep_hours", "water_ml", "blood_glucose_mmol"), "from": ldt("From"), "to": ldt("To")}, "metric"),
		t("log_medication", "Record that a medication dose was taken.",
			P{"name": ls("Medication"), "dose": ls("Dose, e.g. 500 mg"), "taken_at": ldt("When taken (default now)")}, "name", "dose"),
		t("log_workout", "Record a workout.",
			P{"activity": le("Activity", "run", "walk", "cycle", "swim", "strength", "yoga", "other"), "duration_min": ln("Minutes"), "distance_km": ln("Distance"), "calories": ln("kcal"), "time": ldt("When")},
			"activity", "duration_min"),

		// --- shopping list ---
		t("add_to_shopping_list", "Add an item to the shared shopping list.",
			P{"item": ls("Item"), "quantity": ln("Quantity"), "unit": ls("Unit, e.g. kg, bottles")}, "item"),
		t("list_shopping_list", "Show the shopping list.", P{}),
		t("remove_from_shopping_list", "Remove an item from the shopping list.", P{"item_id": ls("Item id")}, "item_id"),

		// --- travel (search only: nothing here can book or pay) ---
		t("search_flights", "Search flights for a date (prices and times). Search only; cannot book.",
			P{"origin": ls("City or IATA code"), "destination": ls("City or IATA code"), "date": ldate("Departure date"), "cabin": le("Cabin", "economy", "premium", "business")}, "origin", "destination", "date"),
		t("get_flight_status", "Live status of a flight by number and date.",
			P{"flight_number": ls("e.g. MU5101"), "date": ldate("Flight date")}, "flight_number", "date"),
		t("search_hotels", "Search hotels in a city for dates. Search only; cannot book.",
			P{"city": ls("City"), "check_in": ldate("Check-in"), "check_out": ldate("Check-out"), "max_price": ln("Max price per night"),
				"filters": lo("Optional filters", P{"min_rating": ln("Minimum guest rating 0-10"), "breakfast": lb("Breakfast included"), "near": ls("Landmark to be near")})},
			"city", "check_in", "check_out"),

		// --- smart home ---
		t("list_devices", "List smart-home devices and their state.", P{"room": ls("Only this room")}),
		t("control_device", "Turn a smart-home device (light, plug, fan, AC) on or off.",
			P{"device_id": ls("Device id"), "action": le("Action", "turn_on", "turn_off", "toggle")}, "device_id", "action"),
		t("set_thermostat", "Set the target temperature and mode of an air conditioner / thermostat.",
			P{"device_id": ls("Device id"), "target_c": ln("Target temperature in °C (16-30)"), "mode": le("Mode", "cool", "heat", "auto", "dry")}, "device_id", "target_c"),

		// --- misc ---
		t("track_package", "Track a parcel by tracking number.",
			P{"tracking_number": ls("Tracking number"), "carrier": le("Carrier (auto-detect if omitted)", "sf", "jd", "ems", "ups", "fedex", "dhl")}, "tracking_number"),
		t("get_user_profile", "The user's profile: name, home and work address, time zone, preferences.", P{}),
	}
}

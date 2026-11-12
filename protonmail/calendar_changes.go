package protonmail

import (
	"fmt"
	"net/http"
)

// Proton keeps a change log per calendar, which its own web client polls. A
// client remembers the id of the last change it saw and asks for everything
// after it, instead of listing the whole calendar again.

// ChangeAction says what happened to an item in the change log.
type ChangeAction int

const (
	ChangeDelete ChangeAction = 0
	ChangeCreate ChangeAction = 1
	ChangeUpdate ChangeAction = 2
)

// CalendarEventChange is one entry in a calendar's change log. Event carries
// the event's metadata only, not its encrypted content; fetch the event by ID
// to get that.
type CalendarEventChange struct {
	ID     string
	Action ChangeAction
	Event  *CalendarEvent
}

// CalendarChanges is a page of a calendar's change log.
type CalendarChanges struct {
	// ChangeID is the position to ask from next time.
	ChangeID string `json:"CalendarModelEventID"`
	// More is 1 when another page follows immediately.
	More int
	// Refresh is non-zero when the log could not be followed and the
	// calendar must be listed again from scratch.
	Refresh int

	CalendarEvents []CalendarEventChange
	// A change to the calendar's keys or settings; the content is not
	// needed, only the fact that something changed.
	CalendarKeys     []struct{ ID string }
	CalendarSettings []struct{ ID string }
}

// KeysOrSettingsChanged reports whether the calendar's keys or settings
// changed in this page.
func (ch *CalendarChanges) KeysOrSettingsChanged() bool {
	return len(ch.CalendarKeys) > 0 || len(ch.CalendarSettings) > 0
}

// GetLatestCalendarChangeID returns the current end of a calendar's change
// log. Changes made after this point are returned by GetCalendarChanges.
func (c *Client) GetLatestCalendarChangeID(calendarID string) (string, error) {
	req, err := c.newRequest(http.MethodGet, calendarPath+"/"+calendarID+"/modelevents/latest", nil)
	if err != nil {
		return "", fmt.Errorf("GetLatestCalendarChangeID: %w", err)
	}

	var respData struct {
		resp
		ChangeID string `json:"CalendarModelEventID"`
	}
	if err := c.doJSON(req, &respData); err != nil {
		return "", fmt.Errorf("GetLatestCalendarChangeID: calendar %s: %w", calendarID, err)
	}
	return respData.ChangeID, nil
}

// GetCalendarChanges returns the changes to a calendar after changeID.
func (c *Client) GetCalendarChanges(calendarID, changeID string) (*CalendarChanges, error) {
	req, err := c.newRequest(http.MethodGet, calendarPath+"/"+calendarID+"/modelevents/"+changeID, nil)
	if err != nil {
		return nil, fmt.Errorf("GetCalendarChanges: %w", err)
	}

	var respData struct {
		resp
		CalendarChanges
	}
	if err := c.doJSON(req, &respData); err != nil {
		return nil, fmt.Errorf("GetCalendarChanges: calendar %s: %w", calendarID, err)
	}
	return &respData.CalendarChanges, nil
}

package state

type Capacity struct {
	Reservations []Reservation `json:"reservations"`
}
type Reservation struct {
	UID      string `json:"uid"`
	GrantUID string `json:"grantUID"`
	Provider string `json:"provider"`
}

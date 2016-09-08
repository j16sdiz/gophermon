package gophermon

import (
	"context"
	"log"
	"time"

	"github.com/pogodevorg/POGOProtos-go"

	"github.com/femot/gophermon/mapsql"
	"github.com/femot/pgoapi-go/api"
	"github.com/femot/pgoapi-go/auth"
)

// ScanDelay
var ScanDelay = 10

// TrainerSession is a helper struct that bundles everything one account can do together.
type TrainerSession struct {
	Provider string
	Username string
	Password string
	Location *api.Location
	Feed     api.Feed
	Session  *api.Session
	Context  context.Context
	Crypto   api.Crypto
}

// Account contains individual account data.
type Account struct {
	Username string
	Password string
	Provider string
}

// NewTrainerSession creates a TrainerSession. The new TrainerSession will not be logged in yet!
func NewTrainerSession(provider, username, password string, location *api.Location, feed api.Feed, crypto api.Crypto) *TrainerSession {
	ctx := context.Background()
	// Return TrainerSession
	return &TrainerSession{
		Provider: provider,
		Username: username,
		Password: password,
		Location: location,
		Feed:     feed,
		Session:  &api.Session{},
		Context:  ctx,
		Crypto:   crypto,
	}
}

// Hunt sends the trainer to scan for pokemon.
// Locations to scan are received from the locations channel and results get sent to the results channel.
// The ticks channel is used for coordination to limit Niantic API calls per second.
func (t *TrainerSession) Hunt(locations chan *api.Location, results chan *protos.GetMapObjectsResponse, ticks chan bool, db mapsql.DbConnection) {
	// Stagger logins, too
	<-ticks
	t.Login()
	for {
		// Check login status
		if t.Session.IsExpired() {
			log.Printf("AuthTicket expired for <%s> creating new session", t.Username)
			for i := 0; i < 5; i++ {
				<-ticks
				err := t.Login()
				if err != nil {
					log.Printf("Login failed for <%s>. (%s)\n", t.Username, err)
					time.Sleep(time.Duration(i * 10))
				} else {
					break
				}
			}
			// Abandon hunt if all retries failed
			if t.Session.IsExpired() {
				log.Printf("All login attempts failed for <%s>. Abandoning hunt.\n", t.Username)
				return
			}
		}
		// Wait for your timeslot to shine!
		<-ticks
		// Try to get new location from channel (wait 30s)
		select {
		case l := <-locations:
			t.Location = l
		case <-time.After(30 * time.Second):
			log.Println("Failed to get new location. Stopping hunt.")
			return
		}
		t.MoveTo(t.Location)
		log.Printf("Hunting at: %f, %f (%s)\n", t.Location.Lat, t.Location.Lon, t.Username)

		// Define a func for requesting map objects. We may need to call this twice per loop
		f := func(a chan *protos.GetMapObjectsResponse, b *TrainerSession) error {
			if r, err := t.GetPlayerMap(); err == nil {
				results <- r
				err = db.AddScannedLocation(t.Location.Lat, t.Location.Lon)
				if err != nil {
					return err
				}
			} else {
				return err
			}
			return nil
		}

		err := f(results, t)
		// Retry after receiving new API URL
		if err != nil && err == api.ErrNewRPCURL {
			// Need to wait before retry
			<-ticks
			err = f(results, t)
		}
		if err != nil {
			log.Println(err)
		}

		// Sleep
		time.Sleep(time.Duration(ScanDelay) * time.Second)
	}
}

// Login initializes a (new) session. This can be used to login again, after the session is expired.
func (t *TrainerSession) Login() error {
	provider, err := auth.NewProvider(t.Provider, t.Username, t.Password)
	if err != nil {
		return err
	}
	session := api.NewSession(provider, t.Location, t.Feed, t.Crypto, false)
	err = session.Init(t.Context)
	if err != nil {
		return err
	}
	t.Session = session
	return nil
}

// LoadTrainers creates TrainerSessions for a slice of Accounts
func LoadTrainers(accounts []Account, feed api.Feed, crypto api.Crypto, startLocation *api.Location) []*TrainerSession {
	trainers := make([]*TrainerSession, 0)
	for _, a := range accounts {
		trainers = append(trainers, NewTrainerSession(a.Provider, a.Username, a.Password, startLocation, feed, crypto))
	}
	return trainers
}

// Wrap session functions for trainer sessions
func (t *TrainerSession) Announce() (*protos.GetMapObjectsResponse, error) {
	return t.Session.Announce(t.Context)
}
func (t *TrainerSession) Call(requests []*protos.Request) (*protos.ResponseEnvelope, error) {
	return t.Session.Call(t.Context, requests)
}
func (t *TrainerSession) GetInventory() (*protos.GetInventoryResponse, error) {
	return t.Session.GetInventory(t.Context)
}
func (t *TrainerSession) GetPlayer() (*protos.GetPlayerResponse, error) {
	return t.Session.GetPlayer(t.Context)
}
func (t *TrainerSession) GetPlayerMap() (*protos.GetMapObjectsResponse, error) {
	return t.Session.GetPlayerMap(t.Context)
}
func (t *TrainerSession) MoveTo(location *api.Location) {
	t.Location = location
	t.Session.MoveTo(location)
}

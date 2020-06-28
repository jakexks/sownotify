package notifier

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gregdel/pushover"
	"github.com/mmcdole/gofeed"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
)

func Run() error {
	err := validateConfig()
	if err != nil {
		return err
	}

	logLock := &sync.Mutex{}
	done := &sync.WaitGroup{}
	quit := make(chan struct{})
	pushChan := make(chan *pushover.Message)
	rssChan := make(chan *rssItem)

	done.Add(3)
	go pusher(done, logLock, pushChan, quit)
	go rssReader(done, logLock, rssChan, quit)
	go dbmanager(done, logLock, rssChan, pushChan, quit)

	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		logLock.Lock()
		log.Info().Msg("Shutting down...")
		logLock.Unlock()
		close(quit)
	}()
	done.Wait()
	return nil
}

func pusher(wg *sync.WaitGroup, logLock *sync.Mutex, push <-chan *pushover.Message, done <-chan struct{}) {
	logfields := make(map[string]interface{})
	logfields["component"] = "pusher"
	logLock.Lock()
	log.Info().Fields(logfields).Msg("Starting pusher")
	logLock.Unlock()

	p := pushover.New(viper.GetString("pushover-app-token"))
	r := pushover.NewRecipient(viper.GetString("pushover-recipient"))

	send := func(message *pushover.Message) {
		if resp, err := p.SendMessage(message, r); err != nil {
			log.Error().Err(err).Send()
		} else {
			logLock.Lock()
			log.Info().
				Fields(logfields).
				Str("id", resp.ID).
				Int("remaining", resp.Limit.Remaining).
				Int("limit", resp.Limit.Remaining).
				Msg("Sent notification")
			logLock.Unlock()
		}
	}

	for {
		select {
		case <-done:
			wg.Done()
			return
		case m := <-push:
			send(m)
		}
	}
}

type rssItem struct {
	generation int
	item       *gofeed.Item
}

func rssReader(wg *sync.WaitGroup, logLock *sync.Mutex, items chan<- *rssItem, done <-chan struct{}) {
	logfields := make(map[string]interface{})
	logfields["component"] = "rssreader"
	logLock.Lock()
	log.Info().Fields(logfields).Msg("Starting rssreader")
	logLock.Unlock()
	generation := 0
	ticker := time.NewTicker(time.Second * time.Duration(viper.GetInt64("poll-interval")))
	parser := gofeed.NewParser()
	grabitems := func() {
		logLock.Lock()
		log.Trace().Fields(logfields).Msg("Checking for new items")
		logLock.Unlock()
		req, err := http.NewRequest(http.MethodGet, viper.GetString("feed-url"), bytes.NewReader([]byte{}))
		if err != nil {
			log.Err(err).Fields(logfields).Msg("failed to make request")
			return
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			logLock.Lock()
			log.Err(err).Fields(logfields).Msg("failed to do request")
			logLock.Unlock()
			return
		}
		defer resp.Body.Close()
		feed, err := parser.Parse(resp.Body)
		if err != nil {
			logLock.Lock()
			log.Err(err).Fields(logfields).Msg("feed parsing failed")
			logLock.Unlock()
			return
		}
		logLock.Lock()
		log.Trace().Fields(logfields).Int("total", len(feed.Items)).Msg("successfully parsed feed")
		logLock.Unlock()
		for _, item := range feed.Items {
			items <- &rssItem{
				generation: generation,
				item:       item,
			}
		}
		generation++
	}
	grabitems()
	for {
		select {
		case <-done:
			wg.Done()
			return
		case <-ticker.C:
			grabitems()
		}
	}
}

func dbmanager(wg *sync.WaitGroup, logLock *sync.Mutex, items <-chan *rssItem, push chan<- *pushover.Message, done <-chan struct{}) {
	logfields := make(map[string]interface{})
	logfields["component"] = "dbmanager"
	logLock.Lock()
	log.Info().Fields(logfields).Msg("Starting dbmanager")
	logLock.Unlock()
	db := make(map[string]*gofeed.Item)
	checkAndAdd := func(item *rssItem) {
		if item.generation == 0 {
			if _, found := db[item.item.GUID]; !found {
				db[item.item.GUID] = item.item
				logLock.Lock()
				log.Trace().Str("title", item.item.Title).Msg("Adding item to db")
				logLock.Unlock()
			}
			return
		}
		if _, found := db[item.item.GUID]; !found {
			logLock.Lock()
			log.Info().Str("title", item.item.Title).Msg("New item found")
			logLock.Unlock()
			db[item.item.GUID] = item.item
			go func(item *gofeed.Item) {
				m := pushover.NewMessageWithTitle("", "sownotify")
				m.HTML = true
				m.Timestamp = time.Now().Unix()
				m.Message = fmt.Sprintf("<b>New torrent:</b> %s<br>\n%s", item.Title, item.Description)
				m.URL = item.Link
				push <- m
			}(item.item)
		}
	}

	for {
		select {
		case <-done:
			wg.Done()
			return
		case i := <-items:
			checkAndAdd(i)
		}
	}
}

func validateConfig() error {
	log.Info().Msg("Validating pushover credentials")
	// validate pushover creds
	p := pushover.New(viper.GetString("pushover-app-token"))
	r := pushover.NewRecipient(viper.GetString("pushover-recipient"))
	m := pushover.NewMessageWithTitle("sownotify is ready to send notifications!", "sownotify")
	_, err := p.SendMessage(m, r)
	if err != nil {
		return err
	}
	// validate feed url
	req, err := http.NewRequest(http.MethodGet, viper.GetString("feed-url"), bytes.NewReader([]byte{}))
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Sownotify/0.1")
	log.Info().Str("host", req.Host).Msg("Validating feed URL")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = gofeed.NewParser().Parse(resp.Body)
	if err != nil {
		return err
	}
	return nil
}

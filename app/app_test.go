package app

import (
	"errors"
	"log"
	"os"
	"time"

	"github.com/alphagov/paas-metric-exporter/events"
	events_mocks "github.com/alphagov/paas-metric-exporter/events/mocks"
	"github.com/alphagov/paas-metric-exporter/metrics"
	metrics_mocks "github.com/alphagov/paas-metric-exporter/metrics/mocks"
	"github.com/alphagov/paas-metric-exporter/processors"
	proc_mocks "github.com/alphagov/paas-metric-exporter/processors/mocks"
	sonde_events "github.com/cloudfoundry/sonde-go/events"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("App", func() {
	var (
		fetcher      *events_mocks.FakeFetcherProcess
		proc1        *proc_mocks.FakeProcessor
		proc2        *proc_mocks.FakeProcessor
		statsdClient *metrics_mocks.FakeStatsdClient
		app          *Application
		appEventChan chan *events.AppEvent
		errorChan    chan error
	)

	BeforeEach(func() {
		log.SetOutput(GinkgoWriter)

		fetcher = &events_mocks.FakeFetcherProcess{}
		proc1 = &proc_mocks.FakeProcessor{}
		proc2 = &proc_mocks.FakeProcessor{}
		processors := map[sonde_events.Envelope_EventType]processors.Processor{
			sonde_events.Envelope_ContainerMetric: proc1,
			sonde_events.Envelope_LogMessage:      proc2,
		}
		appEventChan = make(chan *events.AppEvent, 10)
		errorChan = make(chan error)
		app = &Application{
			config: &Config{
				CFAppUpdateFrequency: time.Second,
			},
			processors:   processors,
			eventFetcher: fetcher,
			sender:       statsdClient,
			appEventChan: appEventChan,
			errorChan:    errorChan,
			exitChan:     make(chan bool),
		}
		go app.Run()
	})

	AfterEach(func() {
		app.Stop()

		log.SetOutput(os.Stdout)
	})

	Context("when started", func() {
		It("starts the fetcher", func() {
			Eventually(func() int {
				return fetcher.RunCallCount()
			}).Should(Equal(1))
		}, 1)
	})

	Context("when a new event is fetched", func() {
		It("an available processor should process it", func() {
			proc1EventType := sonde_events.Envelope_ContainerMetric
			proc2EventType := sonde_events.Envelope_LogMessage
			event1 := &events.AppEvent{Envelope: &sonde_events.Envelope{EventType: &proc1EventType}}
			event2 := &events.AppEvent{Envelope: &sonde_events.Envelope{EventType: &proc2EventType}}

			appEventChan <- event1
			Eventually(func() int {
				return proc1.ProcessCallCount()
			}).Should(Equal(1))
			processedEvent := proc1.ProcessArgsForCall(0)
			Expect(processedEvent).To(Equal(event1))

			appEventChan <- event2
			Eventually(func() int {
				return proc2.ProcessCallCount()
			}).Should(Equal(1))
			processedEvent = proc2.ProcessArgsForCall(0)
			Expect(processedEvent).To(Equal(event2))
		}, 3)

		It("the processed metrics should be sent to the statsd client", func() {
			metric1 := &metrics_mocks.FakeMetric{}
			metric2 := &metrics_mocks.FakeMetric{}
			proc1.ProcessReturnsOnCall(0, []metrics.Metric{metric1, metric2}, nil)

			eventType := sonde_events.Envelope_ContainerMetric
			event := &events.AppEvent{
				Envelope: &sonde_events.Envelope{
					EventType: &eventType,
				},
			}
			appEventChan <- event

			Eventually(func() int {
				return metric1.SendCallCount()
			}).Should(Equal(1))
			Eventually(func() int {
				return metric2.SendCallCount()
			}).Should(Equal(1))

			actualStatsdClient := metric1.SendArgsForCall(0)
			Expect(actualStatsdClient).To(Equal(statsdClient))
			actualStatsdClient = metric2.SendArgsForCall(0)
			Expect(actualStatsdClient).To(Equal(statsdClient))
		}, 3)

		It("should handle metrics sending errors", func() {
			metric1 := &metrics_mocks.FakeMetric{}
			metric1.SendReturnsOnCall(0, errors.New("some sending error"))
			proc1.ProcessReturnsOnCall(0, []metrics.Metric{metric1}, nil)

			metric2 := &metrics_mocks.FakeMetric{}
			proc1.ProcessReturnsOnCall(1, []metrics.Metric{metric2}, nil)

			eventType := sonde_events.Envelope_ContainerMetric
			event1 := &events.AppEvent{Envelope: &sonde_events.Envelope{EventType: &eventType}}
			event2 := &events.AppEvent{Envelope: &sonde_events.Envelope{EventType: &eventType}}
			appEventChan <- event1
			appEventChan <- event2

			Eventually(func() int {
				return metric2.SendCallCount()
			}).Should(Equal(1))
		}, 3)
	})

	Context("when there is no processor for an event", func() {
		It("should be ignored", func() {
			unknownEventType := sonde_events.Envelope_EventType(-1) // non-existent event
			validEventType := sonde_events.Envelope_ContainerMetric
			event1 := &events.AppEvent{Envelope: &sonde_events.Envelope{EventType: &unknownEventType}}
			event2 := &events.AppEvent{Envelope: &sonde_events.Envelope{EventType: &validEventType}}
			appEventChan <- event1
			appEventChan <- event2

			Eventually(func() int {
				return proc1.ProcessCallCount()
			}).Should(Equal(1))

			processedEvent := proc1.ProcessArgsForCall(0)
			Expect(processedEvent).To(Equal(event2))
		})
	})

	Context("when the processor fails to process the event", func() {
		It("should continue to process eventsd", func() {
			proc1.ProcessReturnsOnCall(0, nil, errors.New("some processing error"))
			validEventType := sonde_events.Envelope_ContainerMetric
			event1 := &events.AppEvent{Envelope: &sonde_events.Envelope{EventType: &validEventType}}
			event2 := &events.AppEvent{Envelope: &sonde_events.Envelope{EventType: &validEventType}}
			appEventChan <- event1
			appEventChan <- event2

			Eventually(func() int {
				return proc1.ProcessCallCount()
			}).Should(Equal(2))
		})
	})

	Context("when it receives an error", func() {
		It("should continue to process events", func() {
			errorChan <- errors.New("some error")

			eventType := sonde_events.Envelope_ContainerMetric
			event := &events.AppEvent{Envelope: &sonde_events.Envelope{EventType: &eventType}}
			appEventChan <- event

			Eventually(func() int {
				return proc1.ProcessCallCount()
			}).Should(Equal(1))
		})
	})
})

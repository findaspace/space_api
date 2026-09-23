package metrics

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Metrics owns its own registry rather than using the default one. A package
// that registers into a global on init is a package you cannot test twice in
// the same process.
type Metrics struct {
	Registry *prometheus.Registry
	// registerer       prometheus.Registerer
	RequestDuration  *prometheus.HistogramVec
	RequestsInFlight prometheus.Gauge
	ResponseBytes    *prometheus.HistogramVec

	BookingsHeld    *prometheus.CounterVec
	PaymentsSettled *prometheus.CounterVec
	LedgerImbalance prometheus.Gauge
	JobsProcessed   *prometheus.CounterVec
	SMSSent         *prometheus.CounterVec
}

// Buckets tuned for an API served over Ghanaian mobile networks: the
// interesting range is 50ms to 2s, so that is where the resolution goes.
var durationBuckets = []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10,
}

func New(service string) *Metrics {
	reg := prometheus.NewRegistry()
	labels := prometheus.Labels{"service": service}
	factory := prometheus.WrapRegistererWith(labels, reg)
	// registerer := factory

	m := &Metrics{
		Registry: reg,

		RequestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "space",
			Subsystem: "http",
			Name:      "request_duration_seconds",
			Help:      "Request latency by route template, method and status class.",
			Buckets:   durationBuckets,
		}, []string{"route", "method", "status"}),

		RequestsInFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "space", Subsystem: "http", Name: "requests_in_flight",
			Help: "Requests currently being served.",
		}),

		ResponseBytes: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "space", Subsystem: "http", Name: "response_bytes",
			Help:    "Response size by route template.",
			Buckets: prometheus.ExponentialBuckets(128, 4, 8),
		}, []string{"route"}),

		BookingsHeld: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "space", Subsystem: "booking", Name: "holds_total",
			Help: "Booking hold attempts by rental mode and outcome.",
		}, []string{"mode", "outcome"}),

		PaymentsSettled: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "space", Subsystem: "payment", Name: "settled_total",
			Help: "Payment webhooks applied, by outcome.",
		}, []string{"outcome"}),

		// The one gauge to alert on. Non-zero means something wrote ledger
		// entries outside Post, which should be impossible.
		LedgerImbalance: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "space", Subsystem: "ledger", Name: "imbalance_minor",
			Help: "Trial balance in minor units. Anything but zero is a bug.",
		}),

		JobsProcessed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "space", Subsystem: "worker", Name: "jobs_total",
			Help: "Background jobs by kind and outcome.",
		}, []string{"kind", "outcome"}),

		SMSSent: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "space", Subsystem: "notify", Name: "sms_total",
			Help: "SMS delivery attempts by outcome. Each one costs money.",
		}, []string{"outcome"}),
	}

	factory.MustRegister(
		m.RequestDuration, m.RequestsInFlight, m.ResponseBytes,
		m.BookingsHeld, m.PaymentsSettled, m.LedgerImbalance,
		m.JobsProcessed, m.SMSSent,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return m
}

// WatchPool registers a collector that reads pool statistics at scrape time.
// No background goroutine and no sampling interval to get wrong: the numbers
// are exact as of the moment Prometheus asked.
func (m *Metrics) WatchPool(pool *pgxpool.Pool) {
	m.Registry.MustRegister(&poolCollector{
		pool: pool,
		acquired: prometheus.NewDesc("space_db_connections_acquired",
			"Connections currently checked out of the pool.", nil, nil),
		idle: prometheus.NewDesc("space_db_connections_idle",
			"Connections sitting idle in the pool.", nil, nil),
		total: prometheus.NewDesc("space_db_connections_total",
			"Connections the pool currently holds.", nil, nil),
		max: prometheus.NewDesc("space_db_connections_max",
			"Configured ceiling on pool size.", nil, nil),
		waitCount: prometheus.NewDesc("space_db_acquire_waits_total",
			"Acquires that had to wait for a free connection.", nil, nil),
		waitSeconds: prometheus.NewDesc("space_db_acquire_wait_seconds_total",
			"Cumulative time spent waiting for a connection.", nil, nil),
	})
}

type poolCollector struct {
	pool                       *pgxpool.Pool
	acquired, idle, total, max *prometheus.Desc
	waitCount, waitSeconds     *prometheus.Desc
}

func (c *poolCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.acquired
	ch <- c.idle
	ch <- c.total
	ch <- c.max
	ch <- c.waitCount
	ch <- c.waitSeconds
}

func (c *poolCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.pool.Stat()

	gauge := func(d *prometheus.Desc, v float64) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v)
	}
	counter := func(d *prometheus.Desc, v float64) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.CounterValue, v)
	}

	gauge(c.acquired, float64(s.AcquiredConns()))
	gauge(c.idle, float64(s.IdleConns()))
	gauge(c.total, float64(s.TotalConns()))
	gauge(c.max, float64(s.MaxConns()))

	// A rising wait count with flat latency means the pool is the bottleneck,
	// which is the single most common cause of a slow Go API.
	counter(c.waitCount, float64(s.EmptyAcquireCount()))
	counter(c.waitSeconds, s.AcquireDuration().Seconds())
}

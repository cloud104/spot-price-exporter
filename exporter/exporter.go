package exporter

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

// Exporter implements the prometheus.Exporter interface, and exports AWS Spot Price metrics.
type Exporter struct {
	cfg                 aws.Config
	productDescriptions []string
	regions            []string
	duration           prometheus.Gauge
	scrapeErrors       prometheus.Gauge
	totalScrapes       prometheus.Counter
	spotMetrics        map[string]*prometheus.GaugeVec
	metricsMtx         sync.RWMutex
	sync.RWMutex
}

type scrapeResult struct {
	Name               string
	Value              float64
	Region             string
	AvailabilityZone   string
	InstanceType       string
	ProductDescription string
}

// NewExporter returns a new exporter of AWS Spot Price metrics.
func NewExporter(pds []string, regions []string) (*Exporter, error) {
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		log.WithError(err).Error("Error loading AWS config")
		return nil, err
	}

	e := Exporter{
		cfg:                cfg,
		productDescriptions: pds,
		regions:           regions,
		duration: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "aws_spot",
			Name:      "scrape_duration_seconds",
			Help:      "The scrape duration.",
		}),
		totalScrapes: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "aws_spot",
			Name:      "scrapes_total",
			Help:      "Total AWS autoscaling group scrapes.",
		}),
		scrapeErrors: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "aws_spot",
			Name:      "scrape_error",
			Help:      "The scrape error status.",
		}),
	}

	e.initGauges()
	return &e, nil
}

func (e *Exporter) initGauges() {
	e.spotMetrics = map[string]*prometheus.GaugeVec{}
	e.spotMetrics["current_spot_price"] = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "aws_spot",
		Name:      "current_price",
		Help:      "Current spot price of the instance type.",
	}, []string{"instance_type", "region", "availability_zone", "product_description"})
}

// Describe outputs metric descriptions.
func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	for _, m := range e.spotMetrics {
		m.Describe(ch)
	}
	ch <- e.duration.Desc()
	ch <- e.totalScrapes.Desc()
	ch <- e.scrapeErrors.Desc()
}

// Collect fetches info from the AWS API and the BanzaiCloud recommendation API
func (e *Exporter) Collect(ch chan<- prometheus.Metric) {

	spotScrapes := make(chan scrapeResult)

	e.Lock()
	defer e.Unlock()

	e.initGauges()
	go e.scrape(spotScrapes)
	e.setSpotMetrics(spotScrapes)

	e.duration.Collect(ch)
	e.totalScrapes.Collect(ch)
	e.scrapeErrors.Collect(ch)

	for _, m := range e.spotMetrics {
		m.Collect(ch)
	}
}

func (e *Exporter) scrape(scrapes chan<- scrapeResult) {
	defer close(scrapes)
	now := time.Now().UnixNano()
	e.totalScrapes.Inc()

	var errorCount uint64
	var wg sync.WaitGroup

	ctx := context.Background()

	for _, region := range e.regions {
		log.Debugf("querying spot prices [region=%s]", region)
		wg.Add(1)
		go func(r string) {
			defer wg.Done()

			// Create a new EC2 client for this region
			cfg := e.cfg.Copy()
			cfg.Region = r
			ec2Client := ec2.NewFromConfig(cfg)

			var pds []string
			if len(e.productDescriptions) > 0 {
				pds = e.productDescriptions
			}

			paginator := ec2.NewDescribeSpotPriceHistoryPaginator(ec2Client, &ec2.DescribeSpotPriceHistoryInput{
				StartTime:           aws.Time(time.Now()),
				ProductDescriptions: pds,
			})

			for paginator.HasMorePages() {
				output, err := paginator.NextPage(ctx)
				if err != nil {
					log.WithError(err).Errorf("error while fetching spot price history [region=%s]", r)
					atomic.AddUint64(&errorCount, 1)
					continue
				}

				for _, pe := range output.SpotPriceHistory {
					price, err := strconv.ParseFloat(*pe.SpotPrice, 64)
					if err != nil {
						log.WithError(err).Errorf("error while parsing spot price value from API response [region=%s, az=%s, type=%s]", r, *pe.AvailabilityZone, string(pe.InstanceType))
						atomic.AddUint64(&errorCount, 1)
						continue
					}

					log.Debugf("Creating new metric: current_price{region=%s, az=%s, instance_type=%s, product_description=%s} = %v.", r, *pe.AvailabilityZone, string(pe.InstanceType), string(pe.ProductDescription), price)
					scrapes <- scrapeResult{
						Name:               "current_price",
						Value:              price,
						Region:             r,
						AvailabilityZone:   *pe.AvailabilityZone,
						InstanceType:       string(pe.InstanceType),
						ProductDescription: string(pe.ProductDescription),
					}
				}
			}
		}(region)
	}
	wg.Wait()

	e.scrapeErrors.Set(float64(atomic.LoadUint64(&errorCount)))
	e.duration.Set(float64(time.Now().UnixNano()-now) / 1000000000)
}

func (e *Exporter) setSpotMetrics(scrapes <-chan scrapeResult) {
	log.Debug("set spot metrics")
	for scr := range scrapes {
		name := scr.Name
		if _, ok := e.spotMetrics[name]; !ok {
			e.metricsMtx.Lock()
			e.spotMetrics[name] = prometheus.NewGaugeVec(prometheus.GaugeOpts{
				Namespace: "aws_spot",
				Name:      name,
			}, []string{"instance_type", "region", "availability_zone", "product_description"})
			e.metricsMtx.Unlock()
		}
		var labels prometheus.Labels = map[string]string{
			"instance_type":       scr.InstanceType,
			"region":              scr.Region,
			"availability_zone":   scr.AvailabilityZone,
			"product_description": scr.ProductDescription,
		}
		e.spotMetrics[name].With(labels).Set(float64(scr.Value))
	}
}

func (e *Exporter) inRegions(r string) bool {
	for _, region := range e.regions {
		if r == region {
			return true
		}
	}
	return false
}

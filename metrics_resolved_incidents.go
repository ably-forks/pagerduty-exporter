package main

import (
	"context"
	"fmt"
	"time"

	"github.com/PagerDuty/go-pagerduty"
	"github.com/prometheus/client_golang/prometheus"
	prometheusCommon "github.com/webdevops/go-common/prometheus"
	"github.com/webdevops/go-common/prometheus/collector"
)

type MetricsCollectorResolvedIncidents struct {
	collector.Processor

	prometheus struct {
		incidentTimeToResolve     *prometheus.HistogramVec
		incidentTimeToAcknowledge *prometheus.HistogramVec
	}

	teamListOpt []string
}

func (m *MetricsCollectorResolvedIncidents) Setup(collector *collector.Collector) {

	m.Processor.Setup(collector)

	m.prometheus.incidentTimeToResolve = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "pagerduty_resolved_incident_resolution_time",
			Help: "PagerDuty time to incident resolution histogram",
			Buckets: []float64{
				5 * 60,            // 5 min
				10 * 60,           // 10 min
				15 * 60,           // 15 min
				30 * 60,           // 30 min
				1 * 60 * 60,       // 1 hour
				2 * 60 * 60,       // 2 hour
				6 * 60 * 60,       // 6 hour
				12 * 60 * 60,      // 12 hour
				24 * 60 * 60,      // 1 day
				7 * 24 * 60 * 60,  // 7 days (week)
				31 * 24 * 60 * 60, // 1 month
			},
		},
		[]string{
			"incidentID",
			"serviceID",
			"urgency",
			"escalationPolicy",
		},
	)
	prometheus.MustRegister(m.prometheus.incidentTimeToResolve)

	m.prometheus.incidentTimeToAcknowledge = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "pagerduty_resolved_incident_acknowledged_time",
			Help: "PagerDuty time to incident acknowledgement histogram",
			Buckets: []float64{
				1 * 60,       // 1 min
				2 * 60,       // 2 min
				3 * 60,       // 3 min
				4 * 60,       // 4 min
				5 * 60,       // 5 min
				10 * 60,      // 10 min
				15 * 60,      // 15 min
				30 * 60,      // 30 min
				1 * 60 * 60,  // 1 hour
				2 * 60 * 60,  // 2 hour
				6 * 60 * 60,  // 6 hour
				24 * 60 * 60, // 1 day
			},
		},
		[]string{
			"incidentID",
			"serviceID",
			"urgency",
			"escalationPolicy",
		},
	)
	prometheus.MustRegister(m.prometheus.incidentTimeToAcknowledge)
}

func (m *MetricsCollectorResolvedIncidents) Reset() {
	m.prometheus.incidentTimeToResolve.Reset()
	m.prometheus.incidentTimeToAcknowledge.Reset()
}

func (m *MetricsCollectorResolvedIncidents) Collect(callback chan<- func()) {
	m.collectIncidents(callback)
}

func (m *MetricsCollectorResolvedIncidents) collectIncidents(callback chan<- func()) {

	listOpts := pagerduty.ListIncidentsOptions{}
	listOpts.Statuses = []string{"resolved"}
	listOpts.Offset = 0
	listOpts.SortBy = "created_at:desc"

	if int(opts.PagerDuty.Incident.Limit) < PagerdutyListLimit {
		listOpts.Limit = opts.PagerDuty.Incident.Limit
	} else {
		listOpts.Limit = PagerdutyListLimit
	}

	if len(m.teamListOpt) > 0 {
		listOpts.TeamIDs = m.teamListOpt
	}

	incidentTimeToResolveList := prometheusCommon.NewMetricsList()
	incidentTimeToAcknowledgeList := prometheusCommon.NewMetricsList()

	for {
		m.Logger().Debugf("fetch incidents (offset: %v, limit:%v)", listOpts.Offset, listOpts.Limit)

		list, err := PagerDutyClient.ListIncidents(listOpts)
		PrometheusPagerDutyApiCounter.WithLabelValues("ListIncidents").Inc()

		fmt.Printf("I have received %d incidents\n", len(list.Incidents))

		if err != nil {
			m.Logger().Panic(err)
		}

		for _, incident := range list.Incidents {
			createdAt, _ := time.Parse(time.RFC3339, incident.CreatedAt)
			ctx := context.Background()
			logEntriesOpts := pagerduty.ListIncidentLogEntriesOptions{}
			logsForIncidents, err := PagerDutyClient.ListIncidentLogEntriesWithContext(ctx, incident.ID, logEntriesOpts)
			if err != nil {
				m.Logger().Panic(err)
			}
			var acknowledgedAt time.Time
			var resolvedAt time.Time
			for _, incidentLog := range logsForIncidents.LogEntries {
				if incidentLog.Type == "acknowledge_log_entry" {
					logCreatedAt, _ := time.Parse(time.RFC3339, incidentLog.CreatedAt)
					// get the time of the first acknowledgement, as there can be many
					if acknowledgedAt.IsZero() || logCreatedAt.Before(acknowledgedAt) {
						acknowledgedAt = logCreatedAt
					}
				} else if incidentLog.Type == "resolve_log_entry" {
					resolvedAt, _ = time.Parse(time.RFC3339, incidentLog.CreatedAt)
				}
			}
			timeToResolve := resolvedAt.Sub(createdAt)
			m.Logger().Debugf("incident %s was resolved after %s", incident.ID, timeToResolve.String())
			incidentTimeToResolveList.AddDuration(prometheus.Labels{
				"incidentID":       incident.ID,
				"serviceID":        incident.Service.ID,
				"urgency":          incident.Urgency,
				"escalationPolicy": incident.EscalationPolicy.ID,
			}, timeToResolve)
			m.Logger().Debug("I got here 1")

			if !acknowledgedAt.IsZero() {
				timeToAcknowledge := acknowledgedAt.Sub(createdAt)
				m.Logger().Debugf("incident %s was acknowledged after %s", incident.ID, timeToAcknowledge.String())
				incidentTimeToAcknowledgeList.AddDuration(prometheus.Labels{
					"incidentID":       incident.ID,
					"serviceID":        incident.Service.ID,
					"urgency":          incident.Urgency,
					"escalationPolicy": incident.EscalationPolicy.ID,
				}, timeToAcknowledge)
			}
		}
		m.Logger().Debug("I got here 2")

		listOpts.Offset += PagerdutyListLimit
		if !list.More || listOpts.Offset >= opts.PagerDuty.Incident.Limit {
			break
		}

	}
	m.Logger().Debug("I got here 3")

	callback <- func() {
		incidentTimeToResolveList.HistogramSet(m.prometheus.incidentTimeToResolve)
		incidentTimeToAcknowledgeList.HistogramSet(m.prometheus.incidentTimeToAcknowledge)
	}
}

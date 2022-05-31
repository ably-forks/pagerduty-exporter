package main

import (
	"context"
	"time"

	"github.com/PagerDuty/go-pagerduty"
	"github.com/prometheus/client_golang/prometheus"
	prometheusCommon "github.com/webdevops/go-common/prometheus"
	"github.com/webdevops/go-common/prometheus/collector"
)

type MetricsCollectorIncident struct {
	collector.Processor

	prometheus struct {
		incident                  *prometheus.GaugeVec
		incidentStatus            *prometheus.GaugeVec
		incidentTimeToResolve     *prometheus.HistogramVec
		incidentTimeToAcknowledge *prometheus.HistogramVec
	}

	teamListOpt []string
}

func (m *MetricsCollectorIncident) Setup(collector *collector.Collector) {
	m.Processor.Setup(collector)

	m.prometheus.incident = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "pagerduty_incident_info",
			Help: "PagerDuty incident",
		},
		[]string{
			"incidentID",
			"serviceID",
			"incidentUrl",
			"incidentNumber",
			"title",
			"status",
			"urgency",
			"acknowledged",
			"assigned",
			"type",
			"time",
		},
	)

	m.prometheus.incidentStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "pagerduty_incident_status",
			Help: "PagerDuty incident status",
		},
		[]string{
			"incidentID",
			"userID",
			"time",
			"type",
		},
	)

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

	m.prometheus.incidentTimeToAcknowledge = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "pagerduty_resolved_incident_acknowledgement_time",
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

	prometheus.MustRegister(m.prometheus.incident)
	prometheus.MustRegister(m.prometheus.incidentStatus)
	prometheus.MustRegister(m.prometheus.incidentTimeToResolve)
	prometheus.MustRegister(m.prometheus.incidentTimeToAcknowledge)
}

func (m *MetricsCollectorIncident) Reset() {
	m.prometheus.incident.Reset()
	m.prometheus.incidentStatus.Reset()
}

func (m *MetricsCollectorIncident) Collect(callback chan<- func()) {
	listOpts := pagerduty.ListIncidentsOptions{}
	listOpts.Limit = PagerdutyListLimit
	listOpts.Statuses = opts.PagerDuty.Incident.Statuses
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

	incidentMetricList := prometheusCommon.NewMetricsList()
	incidentStatusMetricList := prometheusCommon.NewMetricsList()
	incidentTimeToResolveList := prometheusCommon.NewMetricsList()
	incidentTimeToAcknowledgeList := prometheusCommon.NewMetricsList()

	for {
		m.Logger().Debugf("fetch incidents (offset: %v, limit:%v)", listOpts.Offset, listOpts.Limit)

		list, err := PagerDutyClient.ListIncidents(listOpts)
		PrometheusPagerDutyApiCounter.WithLabelValues("ListIncidents").Inc()

		if err != nil {
			m.Logger().Panic(err)
		}

		for _, incident := range list.Incidents {
			// info
			createdAt, _ := time.Parse(time.RFC3339, incident.CreatedAt)

			incidentMetricList.AddTime(prometheus.Labels{
				"incidentID":     incident.ID,
				"serviceID":      incident.Service.ID,
				"incidentUrl":    incident.HTMLURL,
				"incidentNumber": uintToString(incident.IncidentNumber),
				"title":          incident.Title,
				"status":         incident.Status,
				"urgency":        incident.Urgency,
				"acknowledged":   boolToString(len(incident.Acknowledgements) >= 1),
				"assigned":       boolToString(len(incident.Assignments) >= 1),
				"type":           incident.Type,
				"time":           createdAt.Format(opts.PagerDuty.Incident.TimeFormat),
			}, createdAt)

			// acknowledgement
			for _, acknowledgement := range incident.Acknowledgements {
				createdAt, _ := time.Parse(time.RFC3339, acknowledgement.At)
				incidentStatusMetricList.AddTime(prometheus.Labels{
					"incidentID": incident.ID,
					"userID":     acknowledgement.Acknowledger.ID,
					"time":       createdAt.Format(opts.PagerDuty.Incident.TimeFormat),
					"type":       "acknowledgement",
				}, createdAt)
			}

			// assignment
			for _, assignment := range incident.Assignments {
				createdAt, _ := time.Parse(time.RFC3339, assignment.At)
				incidentStatusMetricList.AddTime(prometheus.Labels{
					"incidentID": incident.ID,
					"userID":     assignment.Assignee.ID,
					"time":       createdAt.Format(opts.PagerDuty.Incident.TimeFormat),
					"type":       "assignment",
				}, createdAt)
			}

			// lastChange
			changedAt, _ := time.Parse(time.RFC3339, incident.LastStatusChangeAt)
			incidentStatusMetricList.AddTime(prometheus.Labels{
				"incidentID": incident.ID,
				"userID":     incident.LastStatusChangeBy.ID,
				"time":       changedAt.Format(opts.PagerDuty.Incident.TimeFormat),
				"type":       "lastChange",
			}, changedAt)

			// resolution and acknowledgement duration
			if incident.Status == "resolved" {
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
		}

		listOpts.Offset += PagerdutyListLimit
		if !list.More || listOpts.Offset >= opts.PagerDuty.Incident.Limit {
			break
		}
	}

	// set metrics
	callback <- func() {
		incidentMetricList.GaugeSet(m.prometheus.incident)
		incidentStatusMetricList.GaugeSet(m.prometheus.incidentStatus)
		incidentTimeToResolveList.HistogramSet(m.prometheus.incidentTimeToResolve)
		incidentTimeToAcknowledgeList.HistogramSet(m.prometheus.incidentTimeToAcknowledge)
	}
}

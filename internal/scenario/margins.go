package scenario

// The behaviour comparison's margins: how far a timing statistic may be from
// what it is compared with, either way. They are a tolerance of the
// comparison, not something Herald promises, so they are in docs/testing.md,
// "Timing margins", with each one's reason, rather than in the spec
// (decided on #81). TestMarginsFollowTheDocs compares them with it.

// edgeStatistic is the timing the edge recording holds, compared with the
// committed baseline within edgeMargin.
const edgeStatistic = "time since a Delivery's first Attempt"

var edgeMargin = Margin{Percent: 25, Ms: 1000}

// TimingMargins are the timing comparison's margins, by statistic: a
// timing statistic of Statistics and a percentile.
var TimingMargins = map[string]Margin{
	statHandoffLag + " p50":     {Percent: 50, Ms: 50},
	statHandoffLag + " p95":     {Percent: 50, Ms: 50},
	statAttemptLatency + " p50": {Percent: 50, Ms: 50},
	statAttemptLatency + " p95": {Percent: 50, Ms: 50},
	statRetryLateness + " p50":  {Percent: 50, Ms: 50},
	statRetryLateness + " p95":  {Percent: 50, Ms: 50},
}

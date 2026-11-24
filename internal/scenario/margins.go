package scenario

// The behaviour comparison's margins: how far a timing statistic may be from
// what it is compared with. They are a tolerance of the comparison, not
// something Herald promises, so they are in docs/testing.md, "Timing
// margins", with each one's reason, rather than in the spec (decided on
// #81). TestMarginsFollowTheDocs compares them with it.

// edgeStatistic is the timing the edge recording holds, compared with the
// committed baseline within edgeMargin.
const edgeStatistic = "time since a Delivery's first Attempt"

var edgeMargin = Margin{Percent: 25, Ms: 1000}

// TimingMargins are the timing comparison's margins, by what they compare.
var TimingMargins = map[TimingKey]Margin{
	{statHandoffLag, P50}:     {Percent: 10, Ms: 50},
	{statHandoffLag, P95}:     {Percent: 10, Ms: 50},
	{statAttemptLatency, P50}: {Percent: 25, Ms: 20},
	{statAttemptLatency, P95}: {Percent: 25, Ms: 20},
	{statRetryLateness, P50}:  {Percent: 10, Ms: 25},
	{statRetryLateness, P95}:  {Percent: 10, Ms: 25},
}

package discovery

func reportView(report Report, verbose bool) Report {
	if verbose {
		return report
	}
	report.Evidence = nil
	report.Sequences = nil
	report.Attempts = nil
	for i := range report.Endpoints {
		endpoint := &report.Endpoints[i]
		endpoint.Evidence = nil
		hideFieldEvidence(endpoint.Parameters)
		hideFieldEvidence(endpoint.RequestSchema)
		hideFieldEvidence(endpoint.ResponseSchema)
	}
	for _, signals := range [][]Signal{report.Protocols, report.Auth, report.Protections, report.Pagination} {
		for i := range signals {
			signals[i].Evidence = nil
		}
	}
	for i := range report.DataSources {
		report.DataSources[i].Evidence = nil
	}
	return report
}

func hideFieldEvidence(fields []Field) {
	for i := range fields {
		fields[i].Evidence = nil
	}
}

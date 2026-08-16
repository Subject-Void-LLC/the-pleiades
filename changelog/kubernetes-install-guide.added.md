`docs/10-running-in-production.md` now covers installing on Kubernetes: building the
images (nothing is published to any registry), installing the chart, what verification
is and is not available today, and a step-by-step air-gapped install. Its Sizing section
now states the chart's default requests and limits and why they are a starting point
rather than a measurement, and its PKI and TLS section covers the three server-side TLS
arrangements the chart can express.

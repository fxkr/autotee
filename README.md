# autotee

automatic process supervisor and stream multiplexer for video transcoding

It watches an nginx-rtmp server for active streams.  For each active stream,
it spawns a source process, one or more sink processes, and forwards data from
each source to its sinks.  When the stream stops, it kills the sources and
sinks.  When a sink dies or stalls, it restarts it.  When a source dies or
stalls, it restarts it and its sinks.

* Author: `Felix Kaiser <felix.kaiser@fxkr.net>`
* License: MIT license


## Installation

```
go get github.com/fxkr/autotee
go install github.com/fxkr/autotee
```

At compile time this only needs Go and internet access.
At run time it needs GNU screen to be available.
It only runs on Linux.


## FAQ

### Can there be multiple flows for a stream?

Yes.

### Can I use just the service supervision part, not the data forwarding?

Yes.

First, disable the stall detection for sources.
This is currently a global option.
If you need it only for some flows, you'll currently have to use multiple autotee instances.

```
times:
  source_timeout: 0
```

Then use a "fake" source that doesn't terminate unless killed and doesn't produce any output.
Don't use something like `cat` that expects it's stdin to stay open.
The `sleep` command is a good choice.

```
flows:
  "video":
    regexp: "^s\\d+_(native|translated)_(hd|sd)$"
    source: "sleep infinity"
    sinks:
      "sink_1": "sink_1.sh {stream}"
      "sink_2": "sink_2.sh {stream}"
```

### Can I use autotee within a screen?

Yes.


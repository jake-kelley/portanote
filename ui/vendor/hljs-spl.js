/* Splunk SPL grammar for highlight.js (hand-written for Portanote). */
(function () {
  function spl(hljs) {
    const COMMANDS =
      "search stats eval where table fields fieldformat rename sort dedup head tail reverse " +
      "top rare timechart chart trendline predict rex regex erex replace lookup inputlookup " +
      "outputlookup join append appendcols appendpipe selfjoin transaction bin bucket " +
      "streamstats eventstats makeresults tstats mstats mvexpand mvcombine nomv spath xpath " +
      "kv extract multikv fillnull filldown convert addinfo addtotals addcoltotals collect " +
      "delete map foreach return format multisearch union from anomalydetection cluster " +
      "outlier rangemap iplocation geostats geom bucketdir untable xyseriestable xyseries " +
      "makemv makecontinuous accum autoregress delta contingency associate correlate history";
    const FUNCTIONS =
      "count sum avg mean median mode min max stdev stdevp var varp values list dc " +
      "distinct_count estdc first last earliest latest per_second per_minute per_hour per_day " +
      "rate range sumsq if case coalesce validate cidrmatch tostring tonumber typeof round " +
      "ceiling ceil floor abs sqrt pow exp ln log exact sigfig len ltrim rtrim trim lower " +
      "upper substr replace split mvindex mvcount mvfilter mvmap mvjoin mvzip mvappend " +
      "mvdedup mvsort mvrange mvfind strftime strptime relative_time now time strcat like " +
      "match searchmatch isnull isnotnull isnum isint isstr isbool null nullif json " +
      "json_extract json_keys json_valid spath printf md5 sha1 sha256 urldecode";
    const OPERATORS = "AND OR NOT XOR by as sortby output outputnew in over where groupby";
    return {
      name: "SPL",
      aliases: ["splunk"],
      case_insensitive: true,
      keywords: { keyword: COMMANDS, built_in: FUNCTIONS, literal: OPERATORS },
      contains: [
        // SPL comments are ```like this```
        hljs.COMMENT("```", "```"),
        { className: "string", variants: [hljs.QUOTE_STRING_MODE, hljs.APOS_STRING_MODE] },
        hljs.NUMBER_MODE,
        { className: "operator", begin: /\|/ },
        { className: "attr", begin: /[a-zA-Z_][\w.]*(?=\s*=)/ },
      ],
    };
  }
  if (window.hljs) {
    hljs.registerLanguage("spl", spl);
    hljs.registerLanguage("splunk", spl);
  }
})();

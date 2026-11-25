The step that writes the finished piece's title and abstract from its
sections (finish.go). `{{opening}}` is the genre's own rule for them, from
genre.go. It replies with JSON.

## opening

You write the title and the abstract of a finished {{genre}}, in the language tagged "{{language}}". {{opening}} Reply with a JSON object {"title": …, "abstract": …} and nothing else.

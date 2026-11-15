# Third-party notices

aulaglobal-mcp is distributed under the MIT License (see `LICENSE`). It contains
or is based on the third-party works listed here. Their licence texts are
reproduced in full because their licences require it. This file ships in every
release archive and in the Docker image.

## AGDownloader

- Project: https://github.com/Astrak00/AGDownloader
- License: MIT
- Copyright (c) 2024 Eduardo Alarcón

aulaglobal-mcp is an independent project, but the following parts are based on
AGDownloader:

1. The method for obtaining a Moodle mobile token from a browser session: a
   request to `/admin/tool/mobile/launch.php` (`service=moodle_mobile_app`,
   `passport`, `urlscheme=moodlemobile`) carrying the `MoodleSessionag` cookie,
   with the token read from the `moodlemobile://` redirect
   (`TokenFromCookie` in `internal/aulaglobal/cookie.go`).
2. The DevTools console line that reads the `MoodleSessionag` cookie, shown by
   `aulaglobal-mcp setup` (`cmd/aulaglobal-mcp/setup.go`); adapted to copy the
   value to the clipboard instead of logging it.
3. The course-name cleaning logic (`CleanName` in
   `internal/aulaglobal/client.go`), adapted from `extractCourseNameByLanguage`.

License text of AGDownloader, verbatim from its `LICENSE` file:

````text
MIT License

Copyright (c) 2024 Eduardo Alarcón

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
````

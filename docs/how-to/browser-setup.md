# Set it up in your browser

Scryglass is a page you can visit, and that is not the point of it. Three
pieces of browser configuration turn it into the thing you type into, and the
project is only worth running once they are done.

## Make it your new tab page

However your browser does that. The page is built for it: one request, no
external assets, the cursor already in the box.

Firefox and Safari need an extension for a custom new tab page; Chrome and Edge
have a setting. If you would rather not install anything, setting it as your
*homepage* and using the home button is most of the benefit.

## Register it as a search engine

This is the piece people skip, and it is the one that matters most.

Scryglass publishes an OpenSearch descriptor, so your browser can adopt it as a
search engine. Once it has, **everything works from the address bar in every
tab** — not just on the homepage. Typing `!b raid rebuild` in the address bar of some
unrelated page searches your bookmarks.

- **Firefox** — visit the page once, then right-click the address bar and
  choose *Add "Scryglass"*. It appears under Settings → Search.
- **Chrome / Edge** — visit the page once; it is added to Settings → Search
  engines automatically. Give it a short keyword, then type that keyword,
  space, and your query.
- **Safari** — cannot add custom search engines without an extension.

Set it as the *default* engine if you are willing. Everything that does not
match one of your services falls through to whatever engine you configured, so
you lose nothing and gain your own services in the same box.

## Install the bookmarklet

Open `/bookmarks` and expand **Add bookmarks**. There is a link to drag to your
bookmarks bar, and the same address as copyable text.

**Firefox will not accept it by dragging.** It refuses to create a bookmark
from a dragged `javascript:` link, and does so silently — the drag produces
nothing at all, while the link itself works perfectly if you click it. Instead:

1. Right-click your bookmarks toolbar and choose **Add Bookmark**.
2. Give it a name.
3. Paste the address from the copyable field on that page.

Chrome and Safari accept the drag.

Clicking it on any page opens a small window with the address and title already
filled in. It opens a window rather than saving in the background on purpose:
behind an authenticating proxy, an expired session answers a background request
with a redirect to a login page, which fails silently and loses what you were
saving. A window can show you the login page.

package web

// adminPageSize is the shared page size for every paginated admin table
// (users, jobs, audit). It lives outside any single admin page module
// because those pages are installable independently of one another.
const adminPageSize = 20

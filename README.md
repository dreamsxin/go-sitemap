# sitemap

# crawl

### install

```shell
go install github.com/dreamsxin/go-sitemap/cmd/crawl@latest
```

### usage

```shell
crawl -u http://example.com -o sitemap.xml
crawl -u http://example.com -o sitemap.xml -c 1
```

**priority**

`priority.json`

```json
{
  "default": {
    "default": 0.4
  },
  "noquery": {
    "0": 1.0,
    "1": 0.9,
    "2": 0.8,
    "3": 0.6,
    "4": 0.4
  },
  "hasquery": {
    "0": 0.7,
    "1": 0.7,
    "2": 0.4,
    "3": 0.2,
    "4": 0.1
  }
}
```

```shell
crawl -u http://example.com -o sitemap.xml -p priority.json
```

### example

- https://github.com/dreamsxin/go-sitemap/tree/master/cmd/crawl
- https://github.com/dreamsxin/go-seo

## Donation

- [捐贈（Donation）](https://github.com/dreamsxin/cphalcon7/blob/master/DONATE.md)

## License

MIT

-- schema for a tiny blog
CREATE TABLE users (
    id INTEGER NOT NULL,
    email VARCHAR(255) NOT NULL,
    display_name VARCHAR(100),
    PRIMARY KEY (id)
);

/* posts belong to users */
CREATE TABLE IF NOT EXISTS posts (
    id INTEGER PRIMARY KEY,
    author_id INTEGER NOT NULL REFERENCES users(id),
    title VARCHAR(200) NOT NULL,
    body TEXT,
    FOREIGN KEY (author_id) REFERENCES users(id)
);

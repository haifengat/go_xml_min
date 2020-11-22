# go_xml_min

#### 介绍
xml数据->min(pg)

#### 软件架构
xml->tick->min->db


#### 使用说明
* xml文件路径  /xml

#### 环境变量
* pgMin postgres配置
  `postgres://postgres:12345@172.19.129.98:25432/postgres?sslmode=disable`
* xmlSftp
    * 文件所在的sftp配置,不配置则不读取
    * 格式: ip/port/user/password
* xmlSftpPath
    * sftp登录后取xml.tag.gz文件的路径

#### 测试
pgMin=postgresql://postgres:12345@172.19.129.98:20032/postgres?sslmode=disable xmlSftp=172.19.129.98/22/root/Gdqh2018 xmlSftpPath=/mnt/future_xml/ go run main.go -s 20201119

#### 生成镜像
```bash
docker build -t haifengat/go_xml_min:`date +%Y%m%d` .
```
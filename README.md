mysql代理，因为库名包含mysql关键词，从tcp层代理。用户可以直接修改自己mysql账号的密码


docker部署方式

docker run -d --restart=unless-stopped -e TZ=Asia/Shanghai --name=mongo -v /mongodb/db:/data/db -p 27017:27017 -p 5235:5235 mongo --auth


#进入mongo容器执行以下命令创建mongo账号和mysql代理服务的账号密码
db.createUser({ user:'admin',pwd:'123456',roles:[ { role:'userAdminAnyDatabase', db: 'admin'},"readWriteAnyDatabase"]});
#use mysql_proxy;
use mysql;
db.getCollection("user").insertOne({"user":"user1","pass":"123456","db_list":["mac","mysql"]})
db.getCollection("user").insertOne({"user":"user2","pass":"123456","db_list":["mac","mysql"]})
db.getCollection("user").insertOne({"user":"user3","pass":"123456","db_list":["mac","mysql"]})

#run.sh:
#./mysql-server -addr your_mysql_server:3306 -user root -pass pass -mongo mongodb://root:123456@127.0.0.1:27017    
#addr为真实mysql的地址，user为真实mysql的账号，pass为真实mysql的密码，mongo为记录mysql语句和虚拟mysql服务器用户名的mongo数据库地址。
docker run -d --restart=unless-stopped --net=container:mongo -e TZ=Asia/Shanghai --name=mysql_proxy -v ${PWD}:${PWD} -w ${PWD} ubuntu bash run.sh